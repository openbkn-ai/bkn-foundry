#!/bin/bash

while getopts "hmtdbelpk" optname
do
    case "$optname" in
    "m")
        echo "====go generate mock====";
        go generate ./...;;
    "l")
        echo "==== golangci-lint run ===="
        golangci-lint run ./... --exclude-dirs=server/tests;;
    "t")
        echo "====go test -v ./...====";
        go test $(go list ./... | grep -v /server/tests/ | grep -v /server/mocks) -gcflags=all=-l -v ;;
    "d")
        echo "====helm template process ====";
        # Render templates with the helm command.
        # Exit directly if helm does not exist.
        if ! command -v helm &>/dev/null; then
            echo "helm not found, please install helm first" && exit 1
        fi
        helm template ./helm/agent-retrieval -n agent-retrieval -f./helm/agent-retrieval/values.yaml ;;
    "k")
        echo "====使用ktctl连接远程环境====";
        .ktctl/dev.sh;;
    "p")
        echo "==== preview api docs ====";
        cd docs
        ./preview.sh;;
    "b")
        echo "====go build main.go====";
        # Force-overwrite the configuration file.
        config_dir="/sysvol/config"
        secret_dir="/sysvol/secret"
        # Check whether the directory exists.

        mkdir -p "$config_dir" || exit 1
        mkdir -p "$secret_dir" || exit 1

        # Force-copy the configuration file while preserving original file attributes.
        echo "覆盖配置文件到 $config_dir"
        cp -f ./server/infra/config/agent-retrieval.yaml "$config_dir/" || exit 1

        # Force-copy the secret file.
        echo "覆盖密钥文件到 $secret_dir"
        cp -f ./server/infra/config/agent-retrieval-secret.yaml "$secret_dir/" || exit 1

        # Force-copy the observability configuration file.
        echo "覆盖可观测性配置文件到 $config_dir"
        cp -f ./server/infra/config/observability.yaml "$config_dir/" || exit 1

        echo "开始构建运行........."
        go run ./server/main.go;;
    "h")
        echo "-p build and preview api docs";
        echo "-m generate mock";
        echo "-l lint";
        echo "-t test";
        echo "-d helm template";
        echo "-b build main.go";
        echo "-k ktctl connect remote cluster";
        echo "-h help";
        exit 0;;
    esac
done
if [ $# -lt 1 ]; then
    echo "try -h"
fi
