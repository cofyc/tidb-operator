//
// E2E Jenkins file.
//

import groovy.transform.Field

@Field
def podYAML = '''
apiVersion: v1
kind: Pod
spec:
  containers:
  - name: main
    image: gcr.io/k8s-testimages/kubekins-e2e:v20191108-9467d02-master
    command:
    - runner.sh
    - sleep
    - 99d
    # we need privileged mode in order to do docker in docker
    securityContext:
      privileged: true
    env:
    - name: DOCKER_IN_DOCKER_ENABLED
      value: "true"
    resources:
      requests:
        memory: "16000Mi"
        cpu: 8000m
    # kind needs /lib/modules and cgroups from the host
    volumeMounts:
    - mountPath: /lib/modules
      name: modules
      readOnly: true
    - mountPath: /sys/fs/cgroup
      name: cgroup
    # dind expects /var/lib/docker to be volume
    - name: docker-root
      mountPath: /var/lib/docker
    # legacy docker path for cr.io/k8s-testimages/kubekins-e2e
    - name: docker-graph
      mountPath: /docker-graph
  volumes:
  - name: modules
    hostPath:
      path: /lib/modules
      type: Directory
  - name: cgroup
    hostPath:
      path: /sys/fs/cgroup
      type: Directory
  - name: docker-root
    emptyDir: {}
  - name: docker-graph
    emptyDir: {}
'''

def build(SHELL_CODE) {
	podTemplate(yaml: podYAML) {
		node(POD_LABEL) {
			container('main') {
				def WORKSPACE = pwd()
				dir("${WORKSPACE}/go/src/github.com/pingcap/tidb-operator") {
					unstash 'tidb-operator'
					stage("Debug Info") {
						println "debug command: kubectl -n jenkins-ci exec -ti ${NODE_NAME} bash"
					}
					stage('Run') {
						ansiColor('xterm') {
							sh """
							echo "====== shell env ======"
							echo "pwd: \$(pwd)"
							env
							echo "====== go env ======"
							go env
							echo "====== docker version ======"
							docker version
							"""
							sh """
							export GOPATH=${WORKSPACE}/go
							${SHELL_CODE} || true
							sleep 99d
							"""
						}
					}
				}
			}
		}
	}
}

def getChangeLogText() {
	def changeLogText = ""
	for (int i = 0; i < currentBuild.changeSets.size(); i++) {
		for (int j = 0; j < currentBuild.changeSets[i].items.length; j++) {
			def commitId = "${currentBuild.changeSets[i].items[j].commitId}"
			def commitMsg = "${currentBuild.changeSets[i].items[j].msg}"
			changeLogText += "\n" + "`${commitId.take(7)}` ${commitMsg}"
		}
	}
	return changeLogText
}

def call(BUILD_BRANCH, CREDENTIALS_ID, CODECOV_CREDENTIALS_ID) {
	timeout(120) {

	def GITHASH
	def CODECOV_TOKEN
	def UCLOUD_OSS_URL = "http://pingcap-dev.hk.ufileos.com"
	def BUILD_URL = "git@github.com:pingcap/tidb-operator.git"
	def PROJECT_DIR = "go/src/github.com/pingcap/tidb-operator"

	catchError {
		node('build_go1130_memvolume'){
			container("golang") {
				def WORKSPACE = pwd()
				dir("${PROJECT_DIR}"){
					stage('build tidb-operator binary'){
						checkout changelog: false,
						poll: false,
						scm: [
							$class: 'GitSCM',
							branches: [[name: "${BUILD_BRANCH}"]],
							doGenerateSubmoduleConfigurations: false,
							extensions: [],
							submoduleCfg: [],
							userRemoteConfigs: [[
								credentialsId: "${CREDENTIALS_ID}",
								refspec: '+refs/heads/*:refs/remotes/origin/* +refs/pull/*:refs/remotes/origin/pr/*',
								url: "${BUILD_URL}",
							]]
						]

						GITHASH = sh(returnStdout: true, script: "git rev-parse HEAD").trim()
						withCredentials([string(credentialsId: "${CODECOV_CREDENTIALS_ID}", variable: 'codecovToken')]) {
							CODECOV_TOKEN = codecovToken
						}

						ansiColor('xterm') {
						sh """
						export GOPATH=${WORKSPACE}/go
						export PATH=${WORKSPACE}/go/bin:\$PATH
						if ! hash hg 2>/dev/null; then
							sudo yum install -y mercurial
						fi
						hg --version
						#make check-setup
						#make check
						#if [ ${BUILD_BRANCH} == "master" ]
						#then
						#	make test GO_COVER=y
						#	curl -s https://codecov.io/bash | bash -s - -t ${CODECOV_TOKEN} || echo 'Codecov did not collect coverage reports'
						#else
						#	make test
						#fi
						make
						make e2e-build
						"""
						}
					}
					stash excludes: "vendor/**,deploy/**", name: "tidb-operator"
				}
			}
		}

		stage("E2E - v1.12.10") {
			build("IMAGE_TAG=${GITHASH} SKIP_BUILD=y GINKGO_NODES=8 KUBE_VERSION=v1.12.10 make e2e")
		}

		// we requires ~/bin/config.cfg, filemgr-linux64 utilities on k8s-kind node
		// TODO make it possible to run on any node
		node('k8s-kind') {
			dir("${PROJECT_DIR}"){
				deleteDir()
				unstash 'tidb-operator'
				if ( !(BUILD_BRANCH ==~ /[a-z0-9]{40}/) ) {
					stage('upload tidb-operator binary and charts'){
						//upload binary and charts
						sh """
						cp ~/bin/config.cfg ./
						tar -zcvf tidb-operator.tar.gz images/tidb-operator charts
						filemgr-linux64 --action mput --bucket pingcap-dev --nobar --key builds/pingcap/operator/${GITHASH}/centos7/tidb-operator.tar.gz --file tidb-operator.tar.gz
						"""
						//update refs
						writeFile file: 'sha1', text: "${GITHASH}"
						sh """
						filemgr-linux64 --action mput --bucket pingcap-dev --nobar --key refs/pingcap/operator/${BUILD_BRANCH}/centos7/sha1 --file sha1
						rm -f sha1 tidb-operator.tar.gz config.cfg
						"""
					}
				}
			}
		}

		currentBuild.result = "SUCCESS"
	}

	stage('Summary') {
		def CHANGELOG = getChangeLogText()
		def duration = ((System.currentTimeMillis() - currentBuild.startTimeInMillis) / 1000 / 60).setScale(2, BigDecimal.ROUND_HALF_UP)
		def slackmsg = "[#${env.ghprbPullId}: ${env.ghprbPullTitle}]" + "\n" +
		"${env.ghprbPullLink}" + "\n" +
		"${env.ghprbPullDescription}" + "\n" +
		"Integration Common Test Result: `${currentBuild.result}`" + "\n" +
		"Elapsed Time: `${duration} mins` " + "\n" +
		"${CHANGELOG}" + "\n" +
		"${env.RUN_DISPLAY_URL}"

		if (currentBuild.result != "SUCCESS") {
			slackSend channel: '#cloud_jenkins', color: 'danger', teamDomain: 'pingcap', tokenCredentialId: 'slack-pingcap-token', message: "${slackmsg}"
			return
		}

		if ( !(BUILD_BRANCH ==~ /[a-z0-9]{40}/) ){
			slackmsg = "${slackmsg}" + "\n" +
			"Binary Download URL:" + "\n" +
			"${UCLOUD_OSS_URL}/builds/pingcap/operator/${GITHASH}/centos7/tidb-operator.tar.gz"
		}

		slackSend channel: '#cloud_jenkins', color: 'good', teamDomain: 'pingcap', tokenCredentialId: 'slack-pingcap-token', message: "${slackmsg}"
	}

	}
}

return this

// vim: noet
