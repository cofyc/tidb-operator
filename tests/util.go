// Copyright 2018 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.package spec

package tests

import (
	"bytes"
	"fmt"
	"html/template"
	"math/rand"
	"time"

	"github.com/golang/glog"
	"github.com/pingcap/tidb-operator/tests/slack"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	podutil "k8s.io/kubernetes/pkg/api/v1/pod"
	"k8s.io/kubernetes/pkg/client/conditions"
)

// Keep will keep the fun running in the period, otherwise the fun return error
func KeepOrDie(interval time.Duration, period time.Duration, fun func() error) {
	timeline := time.Now().Add(period)
	for {
		if time.Now().After(timeline) {
			break
		}
		err := fun()
		if err != nil {
			slack.NotifyAndPanic(err)
		}
		time.Sleep(interval)
	}
}

func SelectNode(nodes []Nodes) string {
	rand.Seed(time.Now().Unix())
	index := rand.Intn(len(nodes))
	vmNodes := nodes[index].Nodes
	index2 := rand.Intn(len(vmNodes))
	return vmNodes[index2]
}

func GetKubeApiserverPod(kubeCli kubernetes.Interface, node string) (*corev1.Pod, error) {
	return GetPodsByLabels(kubeCli, node, map[string]string{"component": "kube-apiserver"})
}

func GetKubeSchedulerPod(kubeCli kubernetes.Interface, node string) (*corev1.Pod, error) {
	return GetPodsByLabels(kubeCli, node, map[string]string{"component": "kube-scheduler"})
}

func GetKubeControllerManagerPod(kubeCli kubernetes.Interface, node string) (*corev1.Pod, error) {
	return GetPodsByLabels(kubeCli, node, map[string]string{"component": "kube-controller-manager"})
}

func GetKubeDNSPod(kubeCli kubernetes.Interface, node string) (*corev1.Pod, error) {
	return GetPodsByLabels(kubeCli, node, map[string]string{"k8s-app": "kube-dns"})
}

func GetKubeProxyPod(kubeCli kubernetes.Interface, node string) (*corev1.Pod, error) {
	return GetPodsByLabels(kubeCli, node, map[string]string{"k8s-app": "kube-proxy"})
}

func GetPodsByLabels(kubeCli kubernetes.Interface, node string, lables map[string]string) (*corev1.Pod, error) {
	selector := labels.Set(lables).AsSelector()
	options := metav1.ListOptions{LabelSelector: selector.String()}
	componentPods, err := kubeCli.CoreV1().Pods("kube-system").List(options)
	if err != nil {
		return nil, err
	}
	for _, componentPod := range componentPods.Items {
		if componentPod.Spec.NodeName == node {
			return &componentPod, nil
		}
	}
	return nil, nil
}

var affinityTemp string = `{{.Kind}}:
  affinity:
    podAntiAffinity:
      preferredDuringSchedulingIgnoredDuringExecution:
      - weight: {{.Weight}}
        podAffinityTerm:
          labelSelector:
            matchLabels:
              app.kubernetes.io/instance: {{.ClusterName}}
              app.kubernetes.io/component: {{.Kind}}
          topologyKey: "rack"
          namespaces:
          - {{.Namespace}}
`

type AffinityInfo struct {
	ClusterName string
	Kind        string
	Weight      int
	Namespace   string
}

func GetAffinityConfigOrDie(clusterName, namespace string) string {
	temp, err := template.New("dt-affinity").Parse(affinityTemp)
	if err != nil {
		slack.NotifyAndPanic(err)
	}

	pdbuff := new(bytes.Buffer)
	err = temp.Execute(pdbuff, &AffinityInfo{ClusterName: clusterName, Kind: "pd", Weight: 50, Namespace: namespace})
	if err != nil {
		slack.NotifyAndPanic(err)
	}
	tikvbuff := new(bytes.Buffer)
	err = temp.Execute(tikvbuff, &AffinityInfo{ClusterName: clusterName, Kind: "tikv", Weight: 50, Namespace: namespace})
	if err != nil {
		slack.NotifyAndPanic(err)
	}
	tidbbuff := new(bytes.Buffer)
	err = temp.Execute(tidbbuff, &AffinityInfo{ClusterName: clusterName, Kind: "tidb", Weight: 50, Namespace: namespace})
	if err != nil {
		slack.NotifyAndPanic(err)
	}
	return fmt.Sprintf("%s%s%s", pdbuff.String(), tikvbuff.String(), tidbbuff.String())
}

const (
	PodPollInterval = 2 * time.Second
	// PodTimeout is how long to wait for the pod to be started or
	// terminated.
	PodTimeout = 5 * time.Minute
)

type podCondition func(pod *corev1.Pod) (bool, error)

// WaitForPodCondition waits a pods to be matched to the given condition.
func WaitForPodCondition(c kubernetes.Interface, ns, podName, desc string, timeout time.Duration, condition podCondition) error {
	glog.Infof("Waiting up to %v for pod %q in namespace %q to be %q", timeout, podName, ns, desc)
	for start := time.Now(); time.Since(start) < timeout; time.Sleep(PodPollInterval) {
		pod, err := c.CoreV1().Pods(ns).Get(podName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				glog.Infof("Pod %q in namespace %q not found. Error: %v", podName, ns, err)
				return err
			}
			glog.Infof("Get pod %q in namespace %q failed, ignoring for %v. Error: %v", podName, ns, PodPollInterval, err)
			continue
		}
		// log now so that current pod info is reported before calling `condition()`
		glog.Infof("Pod %q: Phase=%q, Reason=%q, readiness=%t. Elapsed: %v",
			podName, pod.Status.Phase, pod.Status.Reason, podutil.IsPodReady(pod), time.Since(start))
		if done, err := condition(pod); done {
			if err == nil {
				glog.Infof("Pod %q satisfied condition %q", podName, desc)
			}
			return err
		}
	}
	return fmt.Errorf("Gave up after waiting %v for pod %q to be %q", timeout, podName, desc)
}

func waitForPodNotFoundInNamespace(c kubernetes.Interface, podName, ns string, timeout time.Duration) error {
	return wait.PollImmediate(PodPollInterval, timeout, func() (bool, error) {
		_, err := c.CoreV1().Pods(ns).Get(podName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil // done
		}
		if err != nil {
			return true, err // stop wait with error
		}
		return false, nil
	})
}

// WaitTimeoutForPodRunningInNamespace waits the given timeout duration for the specified pod to become running.
func WaitTimeoutForPodRunningInNamespace(c kubernetes.Interface, podName, namespace string, timeout time.Duration) error {
	return wait.PollImmediate(PodPollInterval, timeout, podRunning(c, podName, namespace))
}

func podRunning(c kubernetes.Interface, podName, namespace string) wait.ConditionFunc {
	return func() (bool, error) {
		pod, err := c.CoreV1().Pods(namespace).Get(podName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		switch pod.Status.Phase {
		case corev1.PodRunning:
			return true, nil
		case corev1.PodFailed, corev1.PodSucceeded:
			return false, conditions.ErrPodCompleted
		}
		return false, nil
	}
}
