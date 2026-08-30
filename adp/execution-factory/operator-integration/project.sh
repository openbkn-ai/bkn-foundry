#!/bin/bash

while getopts "hmtdbelpk" optname
do
    case "$optname" in
    "m")
        echo "====go generate mock====";
        go generate ./...;;
    "l")
        echo "==== golangci-lint run ===="
        golangci-lint run --exclude-dirs=server/tests --timeout=10m ./... ;;
    "t")
        echo "====go test -v ./...====";
        go test $(go list ./... | grep -v /server/tests | grep -v /server/mocks) -gcflags=all=-l -v ;;
    "d")
        echo "====helm template process ====";
        # Use the helm command to render templates.
        # If helm does not exist, exit directly.
        if ! command -v helm &>/dev/null; then
            echo "helm not found, please install helm first" && exit 1
        fi
        helm template ./helm/agent-operator-integration -n agent-operator-integration -f./helm/agent-operator-integration/values.yaml ;;
    "k")
        echo "====使用ktctl连接远程环境====";
        .ktctl/dev.sh;;
    "p")
        echo "==== preview api docs ====";
        cd docs
        ./preview.sh;;
    "b")
        echo "====go build main.go====";
        # Force overwriting of configuration files.
        config_dir="/sysvol/config"
        secret_dir="/sysvol/secret"
        # Check if directory exists.

        mkdir -p "$config_dir" || exit 1
        mkdir -p "$secret_dir" || exit 1

        # Force copy of configuration file (retain original file attributes)
        echo "覆盖配置文件到 $config_dir"
        cp -f ./server/infra/config/agent-operator-integration.yaml "$config_dir/" || exit 1

        # Force copy of mq configuration file.
        echo "覆盖mq配置文件到 $config_dir"
        cp -f ./server/infra/config/mq_config.yaml "$config_dir/" || exit 1

        # Force copy of key file.
        echo "覆盖密钥文件到 $secret_dir"
        cp -f ./server/infra/config/agent-operator-integration-secret.yaml "$secret_dir/" || exit 1

        # Force copy of observability.yaml.
        echo "覆盖observability.yaml到 $config_dir"
        cp -f ./server/infra/config/observability.yaml "$config_dir/" || exit 1

        echo "$AUTH_ENABLED"

        export AUTH_ENABLED=true

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
