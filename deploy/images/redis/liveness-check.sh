#!/bin/bash

[ -z "${PORT}" ] && exit 1
[ -z "${MASTER_GROUP}" ] && exit 1
[ -z "${MONITOR_USER}" ] && exit 1
[ -z "${MONITOR_PASS_ENCODE}" ] && exit 1

MONITOR_PASS=$(echo -n "${MONITOR_PASS_ENCODE}" | base64 -d | awk '{ print $1 }')
redis_cli_args="-h localhost -p ${PORT} --user ${MONITOR_USER} --pass ${MONITOR_PASS} --no-auth-warning"
response=$(redis-cli ${redis_cli_args} ping)
if [ "${response}" != "PONG" ]; then
    echo "${response}"
    exit 1
fi
echo "response=${response}"

if [[ "${PORT}" == "26379" ]]
then
    port="6379"
    redis_sentinel_cli_args="-h localhost -p ${PORT} --user ${MONITOR_USER} --pass ${MONITOR_PASS} --no-auth-warning"
    redis_cli_args="-h localhost -p ${port} --user ${MONITOR_USER} --pass ${MONITOR_PASS} --no-auth-warning"
elif [[ "${PORT}" == "6379" ]]
then
    port="26379"
    redis_sentinel_cli_args="-h localhost -p ${port} --user ${MONITOR_USER} --pass ${MONITOR_PASS} --no-auth-warning"
    redis_cli_args="-h localhost -p ${PORT} --user ${MONITOR_USER} --pass ${MONITOR_PASS} --no-auth-warning"
fi

redis_sentinel_master_check="$(redis-cli --raw ${redis_sentinel_cli_args} sentinel get-master-addr-by-name ${MASTER_GROUP} | grep 'svc.cluster.local.')"
redis_sentinel_master=${redis_sentinel_master_check}
redis_master_check="$(redis-cli --raw ${redis_cli_args} info replication | grep 'master_host' | tr -d '\r')"
redis_master=${redis_master_check#*:}

if [[ "${redis_sentinel_master}" == "${redis_master}" ]]
then
    echo "redis sentinel monitor master is redis master ${redis_master}"
elif [[ "${redis_sentinel_master}" != "${redis_master}" ]]
then
    echo "redis sentinel monitor master is ${redis_sentinel_master}, but redis master is ${redis_master}"
    redis_master_check="$(redis-cli --raw ${redis_cli_args} info replication | grep 'role' | tr -d '\r')"
    redis_master=${redis_master_check#*:}
    redis_slave_check="$(redis-cli --raw ${redis_cli_args} info replication | grep 'connected_slaves' | tr -d '\r')"
    redis_slave_num=${redis_slave_check#*:}
    if [[ $redis_master == "master" ]] && [[ $(expr $redis_slave_num)+0 > 0 ]]
    then
        echo "redis sentinel monitor master is redis master ${redis_master}"
    else
        echo "redis sentinel monitor master is redis master ${redis_master}, but no enough good replicas, will be restarted"
        exit 1
    fi
else
  echo "redis sentinel monitor master is ${redis_sentinel_master}, not redis master ${redis_master}, will be restarted"
  exit 1
fi