#!/usr/bin/env bash
#
# Publish tidb-operator binaries and charts.
#
# Layout:

#   - /refs/pingcap/operator/${BRANCH}/centos7/sha1
#       git hash of current branch
#   - /builds/pingcap/operator/${GITHASH}/centos7/tidb-operator.tar.gz
#       tarball of charts and binaries and image Dockerfile
#

set -o errexit
set -o nounset
set -o pipefail

ROOT=$(unset CDPATH && cd $(dirname "${BASH_SOURCE[0]}")/.. && pwd)
cd $ROOT

if [ $(uname) != "Linux" ]; then
    echo "error: only linux platform is supported, got $(uname)"
    exit
fi

GITHASH=${GITHASH:-}
BRANCH=${BRANCH:-}

if [[ ! "$GITHASH" =~ ^[0-9a-z]{40}$ ]]; then
    echo "error: invalid value of GITHASH env, should be 40 chars of git hash, got '$GITHASH'"
    exit 1
fi

if [[ -z "$BRANCH" ]]; then
    echo "error: invalid value of BRANCH env, should not be empty, got '$BRANCH'"
    exit 2
fi

tmpfile=$(mktemp)

function clean() {
    echo "info: removing $tmpfile"
    rm -f $tmpfile
    echo "info: removing sha1 tidb-operator.tar.gz config.cfg"
    rm -f sha1 tidb-operator.tar.gz config.cfg
}
trap 'clean' EXIT

FILEMGR_URL="http://tools.ufile.ucloud.com.cn/filemgr-linux64.tar.gz"
echo "info: downloading filemgr-linux64 from $FILEMGR_URL"
echo "curl --retry 10 -L -o - $FILEMGR_URL | tar --strip-components 1 -C $(dirname $tmpfile) -zxf - filemgr-linux64"

exit
tar -zcvf tidb-operator.tar.gz images/tidb-operator charts
$tmpfile --action mput --bucket pingcap-dev --nobar --key builds/pingcap/operator/${GITHASH}/centos7/tidb-operator.tar.gz --file tidb-operator.tar.gz
echo -n "$GITHASH" > sha1
$tmpfile --action mput --bucket pingcap-dev --nobar --key refs/pingcap/operator/${BRANCH}/centos7/sha1 --file sha1
