#!/bin/bash

echo "$(date) Start..."

[ -z "${SERVICE}" ] && exit 1

[ -z "${DOMAIN}" ] && exit 1

[ -z "${REDIS_PORT}" ] && exit 1

[ -z "${SENTINEL_PORT}" ] && exit 1

[ -z "${MASTER_GROUP}" ] && exit 1
[ -z "${QUORUM}" ] && exit 1

[ -z "${SET_DEFAULT_USER}" ] && exit 1

[ -z "${ROOT_USER}" ] && exit 1
[ -z "${ROOT_PASS_ENCODE}" ] && exit 1
ROOT_PASS=$(echo -n "${ROOT_PASS_ENCODE}" | base64 -d | awk '{ print $1 }')

[ -z "${MONITOR_USER}" ] && exit 1
[ -z "${MONITOR_PASS_ENCODE}" ] && exit 1
MONITOR_PASS=$(echo -n "${MONITOR_PASS_ENCODE}" | base64 -d | awk '{ print $1 }')

[ -z "${SENTINEL_USER}" ] && exit 1
[ -z "${SENTINEL_PASS_ENCODE}" ] && exit 1
SENTINEL_PASS=$(echo -n "${SENTINEL_PASS_ENCODE}" | base64 -d | awk '{ print $1 }')

HOSTNAME="$(hostname)"
INDEX="${HOSTNAME##*-}"

REDIS_CONF="/data/conf/redis.conf"
SENTINEL_CONF="/data/conf/sentinel.conf"

USERS_ACL="/data/conf/users.acl"
SENTINEL_ACL="/data/conf/sentinel-users.acl"

MASTER=""

set -eu

sentinel_get_master() {
set +e
    redis-cli -h ${SERVICE} -p ${SENTINEL_PORT} --user ${SENTINEL_USER} --pass ${SENTINEL_PASS} --no-auth-warning --raw \
        sentinel get-master-addr-by-name "${MASTER_GROUP}" | grep -v "${REDIS_PORT}"
set -e
}

sentinel_get_master_retry() {
    master=""
    retry=${1}
    sleep=3
    for i in $(seq 1 "${retry}"); do
        master=$(sentinel_get_master)
        if [ -n "${master}" ]; then
            break
        fi
        sleep $((sleep + i))
    done
    echo "${master}"
}

identify_master() {
    echo "Identifying redis master (get-master-addr-by-name).."
    echo "  using sentinel (${SERVICE}), sentinel group name (${MASTER_GROUP})"
    echo "  $(date).."
    MASTER="$(sentinel_get_master_retry 3)"
    if [ -n "${MASTER}" ]; then
        echo "  $(date) Found redis master (${MASTER})"
    else
        echo "  $(date) Did not find redis master (${MASTER})"
    fi
}

sentinel_update() {
    echo "Updating sentinel config.."
    MY_SENTINEL_ID=$( echo -n "${SERVICE}-${INDEX}" | sha256sum | cut -c1-40 )
    echo "  sentinel id (${MY_SENTINEL_ID}), sentinel grp (${MASTER_GROUP}), quorum (${QUORUM})"
    echo "sentinel myid ${MY_SENTINEL_ID}" >> "${SENTINEL_CONF}"
    echo "  redis master (${1}:${REDIS_PORT})"
    echo "sentinel monitor ${MASTER_GROUP} ${1} ${REDIS_PORT} ${QUORUM}" >> "${SENTINEL_CONF}"
    echo "sentinel announce-ip ${ANNOUNCE_IP}" >> ${SENTINEL_CONF}
    echo "  announce (${ANNOUNCE_IP}:${SENTINEL_PORT})"
    echo "sentinel announce-port ${SENTINEL_PORT}" >> ${SENTINEL_CONF}
}

redis_update() {
    echo "Updating redis config.."
    echo "  we are slave of redis master (${1}:${REDIS_PORT})"
    echo "replicaof ${1} ${REDIS_PORT}" >> "${REDIS_CONF}"
    echo "replica-announce-ip ${ANNOUNCE_IP}" >> ${REDIS_CONF}
    echo "replica-announce-port ${REDIS_PORT}" >> ${REDIS_CONF}
}

copy_config() {
    echo "Copying default redis config.."
    echo "  to '${REDIS_CONF}'"
    cp /readonly-config/redis.conf "${REDIS_CONF}"
    echo "Copying default sentinel config.."
    echo "  to '${SENTINEL_CONF}'"
    cp /readonly-config/sentinel.conf "${SENTINEL_CONF}"

    ROOT_PASS_SHA256=$(echo -n "${ROOT_PASS}" | sha256sum | awk '{ print $1 }')
    SENTINEL_PASS_SHA256=$(echo -n "${SENTINEL_PASS}" | sha256sum | awk '{ print $1 }')

    if [ ! -f "${USERS_ACL}" ]; then
        echo "Copying default users acl file.."
        echo "  to '${USERS_ACL}'"
        cp /readonly-config/users.acl "${USERS_ACL}"

        if [ "$SET_DEFAULT_USER" = "true" ]; then
            echo "user default on #${ROOT_PASS_SHA256} ~* +@all allchannels" >> ${USERS_ACL}
        else
            echo "user default off #${ROOT_PASS_SHA256} ~* +@all allchannels" >> ${USERS_ACL}
        fi

        echo "user ${ROOT_USER} on #${ROOT_PASS_SHA256} ~* +@all allchannels" >> ${USERS_ACL}
        echo "user sentinel-user on #${SENTINEL_PASS_SHA256} allchannels +multi +slaveof +ping +exec +subscribe +config|rewrite +role +publish +info +client|setname +client|kill +script|kill" >> ${USERS_ACL}
        echo "user replica-user on #${SENTINEL_PASS_SHA256} +psync +replconf +ping" >> ${USERS_ACL}
    else
        if [ "$SET_DEFAULT_USER" = "true" ]; then
            sed -i "s/^user default .*/user default on #${ROOT_PASS_SHA256} ~* +@all allchannels/" ${USERS_ACL}
        else
            sed -i "s/^user default .*/user default off #${ROOT_PASS_SHA256} ~* +@all allchannels/" ${USERS_ACL}
        fi
        sed -i "s/^user ${ROOT_USER} .*/user ${ROOT_USER} on #${ROOT_PASS_SHA256} ~* +@all allchannels/" ${USERS_ACL}
        sed -i "s/^user sentinel-user .*/user sentinel-user on #${SENTINEL_PASS_SHA256} allchannels +multi +slaveof +ping +exec +subscribe +config|rewrite +role +publish +info +client|setname +client|kill +script|kill/" ${USERS_ACL}
        sed -i "s/^user monitor-user .*/user monitor-user on #8d580eff577d0a63ab1fa7129f9802c3f995ae2ec1a871ff9f639087c993be8a -@all +ping +@connection +memory -readonly +strlen +config|get +xinfo +pfcount -quit +zcard +type +xlen -readwrite -command +client -wait +scard +llen +hlen +get +eval +slowlog +cluster|info +cluster|slots +cluster|nodes -hello -echo +info +latency +scan -reset -auth -asking/" ${USERS_ACL}
    fi

    if [ ! -f "${SENTINEL_ACL}" ]; then
        echo "Copying default sentinel users acl file.."
        echo "  to '${SENTINEL_ACL}'"
        cp /readonly-config/sentinel-users.acl "${SENTINEL_ACL}"

        if [ "$SET_DEFAULT_USER" = "true" ]; then
            echo "user default on #${ROOT_PASS_SHA256} ~* +@all allchannels" >> ${SENTINEL_ACL}
        else
            echo "user default off #${ROOT_PASS_SHA256} ~* +@all allchannels" >> ${SENTINEL_ACL}
        fi

        echo "user ${ROOT_USER} on #${ROOT_PASS_SHA256} ~* +@all allchannels" >> ${SENTINEL_ACL}
        echo "user sentinel-user on #${SENTINEL_PASS_SHA256} -@all +auth +client|getname +client|id +client|setname +command +hello +ping +role +sentinel|get-master-addr-by-name +sentinel|master +sentinel|myid +sentinel|replicas +sentinel|sentinels +sentinel|masters" >> ${SENTINEL_ACL}
    else
        if [ "$SET_DEFAULT_USER" = "true" ]; then
            sed -i "s/^user default .*/user default on #${ROOT_PASS_SHA256} ~* +@all allchannels/" ${SENTINEL_ACL}
        else
            sed -i "s/^user default .*/user default off #${ROOT_PASS_SHA256} ~* +@all allchannels/" ${SENTINEL_ACL}
        fi
        sed -i "s/^user ${ROOT_USER} .*/user ${ROOT_USER} on #${ROOT_PASS_SHA256} ~* +@all allchannels/" ${SENTINEL_ACL}
        sed -i "s/^user sentinel-user .*/user sentinel-user on #${SENTINEL_PASS_SHA256} -@all +auth +client|getname +client|id +client|setname +command +hello +ping +role +sentinel|get-master-addr-by-name +sentinel|master +sentinel|myid +sentinel|replicas +sentinel|sentinels +sentinel|masters/" ${SENTINEL_ACL}
        sed -i "s/^user monitor-user .*/user monitor-user on #8d580eff577d0a63ab1fa7129f9802c3f995ae2ec1a871ff9f639087c993be8a -@all +ping +@connection -command +client -hello +info -auth +sentinel|masters +sentinel|replicas +sentinel|slaves +sentinel|sentinels +sentinel|ckquorum +sentinel|failover +sentinel|get-master-addr-by-name/" ${SENTINEL_ACL}
    fi
}

setup_defaults() {
    echo "Setting up defaults.."
    echo "  using statefulset index (${INDEX})"
    if [ "${INDEX}" = "0" ]; then
        echo "Setting this pod as master for redis and sentinel.."
        echo "  using announce (${ANNOUNCE_IP})"
        redis_update "${ANNOUNCE_IP}"
        sentinel_update "${ANNOUNCE_IP}"
        echo "  make sure ${ANNOUNCE_IP} is not a slave (slaveof no one)"
        sed -i "s/^.*replicaof.*//" "${REDIS_CONF}"
    else
        echo "Getting redis master ip.."
        DEFAULT_MASTER="${SERVICE}-announce-0.${DOMAIN}"
        echo "  blindly assuming (${DEFAULT_MASTER}) are master"
        echo "Setting default slave config for redis and sentinel.."
        echo "  using master ip (${DEFAULT_MASTER})"
        redis_update "${DEFAULT_MASTER}"
        sentinel_update "${DEFAULT_MASTER}"
    fi
}

redis_ping() {
set +e
    redis_cli_args="-h ${MASTER} -p ${REDIS_PORT} --user ${MONITOR_USER} --pass ${MONITOR_PASS} --no-auth-warning"
    redis-cli ${redis_cli_args} ping
set -e
}

redis_ping_retry() {
    ping=""
    retry=${1}
    sleep=3
    for i in $(seq 1 "${retry}"); do
        if [ "$(redis_ping)" = "PONG" ]; then
            ping="PONG"
            break
        fi
        sleep $((sleep + i))
        MASTER=$(sentinel_get_master)
    done
    echo "${ping}"
}

find_master() {
    echo "Verifying redis master.."
    echo "  ping (${MASTER}:${REDIS_PORT})"
    echo "  $(date).."
    if [ "$(redis_ping_retry 3)" != "PONG" ]; then
        echo "  $(date) Can't ping redis master (${MASTER})"
        echo "Attempting to force failover (sentinel failover).."

        echo "  on sentinel (${SERVICE}:${SENTINEL_PORT}), sentinel grp (${MASTER_GROUP})"
        echo "  $(date).."
        redis_cli_args="${SERVICE} -p ${SENTINEL_PORT} --user ${SENTINEL_USER} --pass ${SENTINEL_PASS} --no-auth-warning --raw"
        if redis-cli ${redis_cli_args} sentinel failover "${MASTER_GROUP}" | grep -q "NOGOODSLAVE" ; then
            echo "  $(date) Failover returned with 'NOGOODSLAVE'"
            echo "Setting defaults for this pod.."
            setup_defaults
            return 0
        fi

        echo "Hold on for 10sec"
        sleep 10
        echo "We should get redis master's ip now. Asking (get-master-addr-by-name).."
        echo "  sentinel (${SERVICE}:${SENTINEL_PORT}), sentinel grp (${MASTER_GROUP})"
        echo "  $(date).."
        MASTER="$(sentinel_get_master)"
        if [ "${MASTER}" ]; then
            echo "  $(date) Found redis master (${MASTER})"
            echo "Updating redis and sentinel config.."
            redis_update "${MASTER}"
            sentinel_update "${MASTER}"

            if [ "${MASTER}" = "${ANNOUNCE_IP}" ]; then
                sed -i "s/^.*replicaof.*//" "${REDIS_CONF}"
            fi
        else
            echo "$(date) Error: Could not failover, exiting..."
            exit 1
        fi
    else
        echo "  $(date) Found reachable redis master (${MASTER})"
        echo "Updating redis and sentinel config.."
        redis_update "${MASTER}"
        sentinel_update "${MASTER}"
    fi
}

check_aof() {
    AOF_DIR="/data/appendonlydir"
    if [ -d "${AOF_DIR}" ]; then
        echo "检测到 AOF 目录 ${AOF_DIR}，开始检查所有 AOF 文件..."
        for aof in "${AOF_DIR}"/*.aof; do
            [ -e "${aof}" ] || continue
            echo "检查文件: ${aof}"
            if ! redis-check-aof "${aof}" >/dev/null 2>&1; then
                echo "  文件损坏，备份并修复..."
                cp "${aof}" "${aof}.bak.$(date +%s)"
                echo "y" | redis-check-aof --fix "${aof}"
            else
                echo "  文件正常"
            fi
        done
        echo "AOF 检查完成"
    else
        echo "AOF 目录 ${AOF_DIR} 不存在，跳过检查"
    fi
}

mkdir -p /data/conf/

echo "Initializing config.."
copy_config

# where is redis master
identify_master

echo "Identify announce ip for this pod.."
ANNOUNCE_IP="${SERVICE}-announce-${INDEX}.${DOMAIN}"
echo "  identified announce (${ANNOUNCE_IP})"
if [ "${MASTER}" ]; then
    find_master
else
    setup_defaults
fi

check_aof

echo "$(date) Ready..."