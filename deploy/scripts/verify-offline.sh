#!/bin/bash
# 离线环境验证脚本
# 用于检查离线部署所需的所有组件是否已安装

set -e

echo "=========================================="
echo "   离线环境验证脚本"
echo "=========================================="
echo ""

# 设置离线仓库地址（可从环境变量覆盖）
OFFLINE_REGISTRY="${OFFLINE_REGISTRY:-registry.openbkn.ai:5000}"

# 检查函数
check_command() {
    local cmd="$1"
    local package="$2"

    if command -v "$cmd" &>/dev/null; then
        local version
        version=$($cmd --version 2>/dev/null | head -1 || $cmd version 2>/dev/null | head -1 || echo "unknown")
        echo "✓ $cmd: $(command -v $cmd) ($version)"
        return 0
    else
        echo "✗ $cmd: 未安装"
        echo "  安装方法: yum install -y $package"
        return 1
    fi
}

check_service() {
    local service="$1"
    if systemctl is-active --quiet "$service" 2>/dev/null; then
        echo "✓ $service: 运行中"
        return 0
    else
        echo "✗ $service: 未运行"
        return 1
    fi
}

# 系统检查
echo "1. 系统组件检查"
echo "----------------------------------------"
ERRORS=0

check_command kubeadm kubeadm || ERRORS=$((ERRORS + 1))
check_command kubelet kubelet || ERRORS=$((ERRORS + 1))
check_command kubectl kubectl || ERRORS=$((ERRORS + 1))
check_command containerd containerd.io || ERRORS=$((ERRORS + 1))
check_command crictl cri-tools || ERRORS=$((ERRORS + 1))
check_command helm helm || ERRORS=$((ERRORS + 1))
check_command python3 python3 || ERRORS=$((ERRORS + 1))

echo ""
echo "2. 服务状态检查"
echo "----------------------------------------"
check_service containerd || ERRORS=$((ERRORS + 1))

echo ""
echo "3. CNI 插件检查"
echo "----------------------------------------"
if [[ -x /opt/cni/bin/loopback ]]; then
    echo "✓ CNI 插件: /opt/cni/bin/loopback 存在"
    ls -la /opt/cni/bin/ | head -5
else
    echo "✗ CNI 插件: /opt/cni/bin/loopback 不存在"
    echo "  安装方法: yum install -y kubernetes-cni"
    ERRORS=$((ERRORS + 1))
fi

echo ""
echo "4. 镜像仓库连接性检查"
echo "----------------------------------------"
echo "离线仓库地址: $OFFLINE_REGISTRY"

if curl -s --connect-timeout 5 "http://${OFFLINE_REGISTRY}/v2/" &>/dev/null; then
    echo "✓ 离线镜像仓库可访问 (HTTP)"

    # 检查关键镜像
    echo ""
    echo "检查关键镜像:"
    IMAGES=(
        "google_containers/kube-apiserver"
        "google_containers/kube-proxy"
        "google_containers/pause"
        "flannel/flannel"
        "ingress-nginx/controller"
    )

    for img in "${IMAGES[@]}"; do
        if curl -s "http://${OFFLINE_REGISTRY}/v2/${img}/tags/list" | grep -q "tags"; then
            echo "  ✓ $img"
        else
            echo "  ✗ $img (未找到)"
        fi
    done
else
    echo "✗ 离线镜像仓库不可访问"
    echo "  请检查仓库地址和网络连接"
    ERRORS=$((ERRORS + 1))
fi

echo ""
echo "5. 系统配置检查"
echo "----------------------------------------"

# 检查 Swap
if swapon --show | grep -q .; then
    echo "✗ Swap: 已启用（需要关闭）"
    ERRORS=$((ERRORS + 1))
else
    echo "✓ Swap: 已关闭"
fi

# 检查 IP 转发
if [[ "$(cat /proc/sys/net/ipv4/ip_forward)" == "1" ]]; then
    echo "✓ IP 转发: 已启用"
else
    echo "✗ IP 转发: 未启用"
    ERRORS=$((ERRORS + 1))
fi

# 检查防火墙
if systemctl is-active --quiet firewalld 2>/dev/null; then
    echo "⚠ 防火墙: firewalld 运行中（建议关闭）"
elif systemctl is-active --quiet ufw 2>/dev/null; then
    echo "⚠ 防火墙: ufw 运行中（建议关闭）"
else
    echo "✓ 防火墙: 未运行"
fi

echo ""
echo "=========================================="
if [[ $ERRORS -eq 0 ]]; then
    echo "✓ 所有检查通过！环境已就绪"
    echo ""
    echo "可以开始离线部署："
    echo "  sudo bash ./deploy.sh --offline k8s install"
else
    echo "✗ 发现 $ERRORS 个问题，请先解决"
    echo ""
    echo "参考文档: docs/offline-prerequisites.zh.md"
fi
echo "=========================================="

exit $ERRORS