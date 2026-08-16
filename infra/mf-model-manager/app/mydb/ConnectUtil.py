from confluent_kafka import Producer, Consumer
from confluent_kafka.admin import AdminClient, NewTopic

from app.core.config import base_config, resolve_metering_backend
from app.logs.stand_log import StandLogger

import redis
import redis.asyncio as redis_async
from redis.asyncio.sentinel import Sentinel as Sentinel_async
from redis.sentinel import Sentinel
from typing import Union, Dict, List
import asyncio
import time


class RedisClient(object):
    _instance = None
    _lock = asyncio.Lock()

    def __new__(cls):
        if not cls._instance:
            cls._instance = super(RedisClient, cls).__new__(cls)
        return cls._instance

    def __init__(self):
        if not hasattr(self, '_initialized'):
            self.redis_cluster_mode = base_config.REDISCLUSTERMODE
            self.redis_ip = base_config.REDISHOST
            self.redis_read_ip = base_config.REDISREADHOST
            self.redis_read_port = base_config.REDISREADPORT
            self.redis_read_user = base_config.REDISREADUSER
            self.redis_read_passwd = base_config.REDISREADPASS
            self.redis_write_ip = base_config.REDISWRITEHOST
            self.redis_write_port = base_config.REDISWRITEPORT
            self.redis_write_user = base_config.REDISWRITEUSER
            self.redis_write_passwd = base_config.REDISWRITEPASS
            self.redis_port = base_config.REDISPORT
            self.redis_user = ""
            if base_config.REDISUSER:
                self.redis_user = base_config.REDISUSER
            self.redis_passwd = base_config.REDISPASS
            self.redis_master_name = base_config.SENTINELMASTER
            self.redis_sentinel_user = base_config.SENTINELUSER
            self.redis_sentinel_password = base_config.SENTINELPASS
            self._initialized = True

    async def connect_redis(self, db, model):
        assert model in ["read", "write"]
        if self.redis_cluster_mode == "sentinel":
            try:
                sentinel = Sentinel_async([(self.redis_ip, self.redis_port)], password=self.redis_sentinel_password,
                                          sentinel_kwargs={
                                              "password": self.redis_sentinel_password,
                                              "username": self.redis_sentinel_user
                                          })
                if model == "write":
                    redis_con = await sentinel.master_for(self.redis_master_name, username=self.redis_user,
                                                          password=self.redis_passwd, db=db)
                if model == "read":
                    redis_con = await sentinel.slave_for(self.redis_master_name, username=self.redis_user,
                                                         password=self.redis_passwd, db=db)
                await redis_con.ping()
                return redis_con
            except Exception as e:
                StandLogger.error(f"Redis连接失败 - sentinel模式: host={self.redis_ip}, port={self.redis_port}, error={str(e)}")
                raise Exception(f"connect redis error:{str(e)}")
        elif self.redis_cluster_mode == "master-slave":
            try:
                if model == "read":
                    pool = redis_async.ConnectionPool(host=self.redis_read_ip, port=self.redis_read_port, db=db,
                                                      password=self.redis_read_passwd)
                    redis_con = redis_async.StrictRedis(connection_pool=pool)
                if model == "write":
                    pool = redis_async.ConnectionPool(host=self.redis_write_ip, port=self.redis_write_port, db=db,
                                                      password=self.redis_write_passwd)
                    redis_con = redis_async.StrictRedis(connection_pool=pool)
                await redis_con.ping()
                return redis_con
            except Exception as e:
                StandLogger.error(f"Redis连接失败 - master-slave模式: host={self.redis_ip}, port={self.redis_port}, error={str(e)}")
                raise Exception(f"connect redis error:{str(e)}")
        try:
            pool = redis_async.ConnectionPool(host=self.redis_ip, port=self.redis_port, db=db,
                                              password=self.redis_passwd,
                                              username=self.redis_user if self.redis_user else None)
            redis_con = redis_async.StrictRedis(connection_pool=pool)
            await redis_con.ping()
            return redis_con
        except Exception as e:
            StandLogger.error(f"Redis连接失败 - standalone模式: host={self.redis_ip}, port={self.redis_port}, error={str(e)}")
            raise Exception(f"connect redis error:{str(e)}")

    async def connect_redis_async(self, db, model):
        assert model in ["read", "write"]
        if self.redis_cluster_mode == "sentinel":
            try:
                sentinel = Sentinel_async([(self.redis_ip, self.redis_port)], password=self.redis_sentinel_password,
                                          sentinel_kwargs={
                                              "password": self.redis_sentinel_password,
                                              "username": self.redis_sentinel_user
                                          })
                if model == "write":
                    redis_con = await sentinel.master_for(self.redis_master_name, username=self.redis_user,
                                                          password=self.redis_passwd, db=db)
                if model == "read":
                    redis_con = await sentinel.slave_for(self.redis_master_name, username=self.redis_user,
                                                         password=self.redis_passwd, db=db)
                await redis_con.ping()
                return redis_con
            except Exception as e:
                StandLogger.error(f"Redis连接失败 - {model}模式: host={self.redis_ip}, port={self.redis_port}, error={str(e)}")
                raise Exception(f"connect redis error:{str(e)}")
        elif self.redis_cluster_mode == "master-slave":
            try:
                if model == "read":
                    pool = redis_async.ConnectionPool(host=self.redis_read_ip, port=self.redis_read_port, db=db,
                                                      password=self.redis_read_passwd)
                    redis_con = redis_async.StrictRedis(connection_pool=pool)
                if model == "write":
                    pool = redis_async.ConnectionPool(host=self.redis_write_ip, port=self.redis_write_port, db=db,
                                                      password=self.redis_write_passwd)
                    redis_con = redis_async.StrictRedis(connection_pool=pool)
                await redis_con.ping()
                return redis_con
            except Exception as e:
                StandLogger.error(f"Redis连接失败 - {model}模式: host={self.redis_ip}, port={self.redis_port}, error={str(e)}")
                raise Exception(f"connect redis error:{str(e)}")
        else:
            try:
                pool = redis_async.ConnectionPool(host=self.redis_ip, port=self.redis_port, db=db,
                                                  password=self.redis_passwd, username=self.redis_user if self.redis_user else None)
                redis_con = redis_async.StrictRedis(connection_pool=pool)
                await redis_con.ping()
                return redis_con
            except Exception as e:
                StandLogger.error(f"Redis连接失败 - standalone模式: host={self.redis_ip}, port={self.redis_port}, error={str(e)}")
                raise Exception(f"connect redis error:{str(e)}")


class ConnectUtil:
    _instance = None
    _lock = asyncio.Lock()
    _redis_client = None
    _read_pool = None
    _write_pool = None

    @classmethod
    async def create(cls, db=1):
        async with cls._lock:
            if cls._instance is None:
                instance = cls(db)
                if cls._redis_client is None:
                    cls._redis_client = RedisClient()
                await instance._init_connection_pools()
                if instance.read_conn is None or instance.write_conn is None:
                    StandLogger.error(f"Redis连接创建失败: read_conn={instance.read_conn}, write_conn={instance.write_conn}")
                    raise Exception("Failed to create the Redis connection")
                instance._initialized = True
                StandLogger.info(f"Redis连接池创建成功: read_conn={instance.read_conn}, write_conn={instance.write_conn}")
                cls._instance = instance
            return cls._instance

    def __init__(self, db=1):
        self.db = db
        self.read_conn = None
        self.write_conn = None
        self._connection_healthy = True
        self._last_health_check = 0

    async def _init_connection_pools(self):
        """Initialize the Redis connection pools."""
        try:
            redis_client = self.__class__._redis_client
            
            StandLogger.info(f"开始初始化Redis连接池, 模式: {redis_client.redis_cluster_mode}, "
                           f"host: {redis_client.redis_ip}, port: {redis_client.redis_port}, "
                           f"user: {redis_client.redis_user}")
            
            if redis_client.redis_cluster_mode == "sentinel":
                StandLogger.info("使用哨兵模式初始化")
                await self._init_sentinel_connection_pools(redis_client)
            elif redis_client.redis_cluster_mode == "master-slave":
                StandLogger.info("使用主从模式初始化")
                await self._init_master_slave_connection_pools(redis_client)
            else:
                StandLogger.info(f"使用默认模式初始化 (实际模式: {redis_client.redis_cluster_mode})")
                await self._init_default_connection_pools(redis_client)
            
            if self.read_conn is None:
                raise Exception(f"Failed to create the read connection: read_conn is None")
            if self.write_conn is None:
                raise Exception(f"Failed to create the write connection: write_conn is None")
            
            StandLogger.info(f"Redis连接池创建成功: read_conn={self.read_conn}, write_conn={self.write_conn}")
            
            await self._warmup_connections()
            
            StandLogger.info("Redis高性能连接池初始化完成")
            
        except Exception as e:
            StandLogger.error(f"Redis连接池初始化失败: {str(e)}")
            import traceback
            StandLogger.error(f"详细错误: {traceback.format_exc()}")
            raise e

    async def _init_sentinel_connection_pools(self, redis_client):
        """Initialize Sentinel connection pools."""
        try:
            sentinel = Sentinel_async([(redis_client.redis_ip, redis_client.redis_port)], 
                                    password=redis_client.redis_sentinel_password,
                                    sentinel_kwargs={
                                        "password": redis_client.redis_sentinel_password,
                                        "username": redis_client.redis_sentinel_user
                                    })
            
            master_redis = await sentinel.master_for(
                redis_client.redis_master_name, 
                username=redis_client.redis_user,
                password=redis_client.redis_passwd, 
                db=self.db,
                max_connections=30,
                retry_on_timeout=True,
                socket_keepalive=True,
                socket_connect_timeout=3,
                socket_timeout=2,
                health_check_interval=30,
                decode_responses=False
            )
            
            slave_redis = await sentinel.slave_for(
                redis_client.redis_master_name, 
                username=redis_client.redis_user,
                password=redis_client.redis_passwd, 
                db=self.db,
                max_connections=50,
                retry_on_timeout=True,
                socket_keepalive=True,
                socket_connect_timeout=3,
                socket_timeout=2,
                health_check_interval=30,
                decode_responses=False
            )
            
            self.write_conn = master_redis
            self.read_conn = slave_redis
            
            StandLogger.info("哨兵模式Redis连接池初始化成功")
            
        except Exception as e:
            StandLogger.error(f"哨兵模式Redis连接池初始化失败: {str(e)}")
            raise e

    async def _init_master_slave_connection_pools(self, redis_client):
        """Initialize primary-replica connection pools."""
        try:
            self.__class__._read_pool = redis_async.ConnectionPool(
                host=redis_client.redis_read_ip,
                port=redis_client.redis_read_port,
                db=self.db,
                password=redis_client.redis_read_passwd,
                max_connections=50,  # Maximum connections.
                retry_on_timeout=True,
                socket_keepalive=True,
                socket_connect_timeout=3,  # Shorter connection timeout.
                socket_timeout=2,  # Shorter operation timeout.
                health_check_interval=30,  # Health-check interval.
                decode_responses=False  # Keep binary responses for performance.
            )
            
            self.__class__._write_pool = redis_async.ConnectionPool(
                host=redis_client.redis_write_ip,
                port=redis_client.redis_write_port,
                db=self.db,
                password=redis_client.redis_write_passwd,
                max_connections=30,  # Fewer connections are needed for writes.
                retry_on_timeout=True,
                socket_keepalive=True,
                socket_connect_timeout=3,  # Shorter connection timeout.
                socket_timeout=2,  # Shorter operation timeout.
                health_check_interval=30,
                decode_responses=False
            )
            
            self.read_conn = redis_async.StrictRedis(connection_pool=self.__class__._read_pool)
            self.write_conn = redis_async.StrictRedis(connection_pool=self.__class__._write_pool)
            
            StandLogger.info("主从模式Redis连接池初始化成功")
            
        except Exception as e:
            StandLogger.error(f"主从模式Redis连接池初始化失败: {str(e)}")
            raise e

    async def _init_default_connection_pools(self, redis_client):
        """Initialize the standalone connection pool."""
        try:
            StandLogger.info(f"创建默认模式连接池: host={redis_client.redis_ip}, "
                           f"port={redis_client.redis_port}, db={self.db}")
            
            self.__class__._read_pool = redis_async.ConnectionPool(
                host=redis_client.redis_ip,
                port=redis_client.redis_port,
                db=self.db,
                password=redis_client.redis_passwd,
                max_connections=50,  # Maximum connections.
                retry_on_timeout=True,
                socket_keepalive=True,
                socket_connect_timeout=3,  # Shorter connection timeout.
                socket_timeout=2,  # Shorter operation timeout.
                health_check_interval=30,  # Health-check interval.
                decode_responses=False  # Keep binary responses for performance.
            )
            
            self.read_conn = redis_async.StrictRedis(connection_pool=self.__class__._read_pool)
            self.write_conn = redis_async.StrictRedis(connection_pool=self.__class__._read_pool)
            
            StandLogger.info(f"默认模式Redis连接池初始化成功: read_conn={self.read_conn}, write_conn={self.write_conn}")
            
        except Exception as e:
            StandLogger.error(f"默认模式Redis连接池初始化失败: {str(e)}")
            import traceback
            StandLogger.error(f"详细错误: {traceback.format_exc()}")
            raise e

    async def _warmup_connections(self):
        """Warm the connection pool."""
        try:
            redis_client = self.__class__._redis_client
            
            if redis_client.redis_cluster_mode == "sentinel":
                await self.read_conn.ping()
                await self.write_conn.ping()
            else:
                if self.__class__._read_pool:
                    for _ in range(min(5, self.__class__._read_pool.max_connections)):
                        await self.read_conn.ping()
                
                if self.__class__._write_pool:
                    for _ in range(min(3, self.__class__._write_pool.max_connections)):
                        await self.write_conn.ping()
                
            StandLogger.info("Redis连接池预热完成")
        except Exception as e:
            StandLogger.warn(f"Redis连接池预热失败: {str(e)}")

    async def _check_connection_health(self):
        """Check connection health."""
        current_time = time.time()
        if current_time - self._last_health_check > 30:
            try:
                await self.read_conn.ping()
                await self.write_conn.ping()
                self._connection_healthy = True
                self._last_health_check = current_time
            except Exception as e:
                StandLogger.warn(f"Redis连接健康检查失败: {str(e)}")
                self._connection_healthy = False
        return self._connection_healthy

    async def _reconnect(self):
        """Reconnect to Redis only when required."""
        if self._connection_healthy:
            return
            
        StandLogger.info("开始重新建立Redis连接...")
        old_read_conn = self.read_conn
        old_write_conn = self.write_conn
        
        try:
            await self._init_connection_pools()
            self._connection_healthy = True
            StandLogger.info("Redis连接重建成功")
            
            if old_read_conn:
                try:
                    await old_read_conn.close()
                except:
                    pass
            if old_write_conn:
                try:
                    await old_write_conn.close()
                except:
                    pass
                    
        except Exception as e:
            StandLogger.error(f"Redis重连失败: {str(e)}")
            self.read_conn = old_read_conn
            self.write_conn = old_write_conn
            raise e

    async def close(self):
        """Close Redis connections."""
        if self.read_conn:
            await self.read_conn.close()
        if self.write_conn:
            await self.write_conn.close()
        self.read_conn = None
        self.write_conn = None

    async def set_str(self, key: str, value: Union[str, int, float], expire: int = None) -> bool:
        for i in range(3):
            try:
                result = await self.write_conn.set(key, value)
                return result and await self._set_expire(key, expire)
            except Exception as e:
                if i < 2:
                    await asyncio.sleep(0.1)
                    await self._reconnect()
                else:
                    raise e

    async def set_if_absent(self, key: str, value: Union[str, int, float] = 1, expire: int | None = None) -> bool:
        """Apply SETNX semantics with an optional expiration."""
        for i in range(3):
            try:
                # Redis-py: set(name, value, ex=None, px=None, nx=False, xx=False)
                result = await self.write_conn.set(key, value, ex=expire, nx=True)
                return bool(result)
            except Exception as e:
                if i < 2:
                    await asyncio.sleep(0.1)
                    await self._reconnect()
                else:
                    raise e

    async def _set_expire(self, key: str, expire: int = None) -> bool:
        """Set a key expiration."""
        if expire is not None:
            return await self.write_conn.expire(key, expire)
        return True

    async def lpush(self, key: str, *values: Union[str, int, float], expire: int = None) -> int:
        for i in range(3):
            try:
                result = await self.write_conn.lpush(key, *values)
                await self._set_expire(key, expire)
                return result
            except Exception as e:
                if i < 2:
                    await asyncio.sleep(0.1)
                    await self._reconnect()
                else:
                    raise e

    async def rpush(self, key: str, *values: Union[str, int, float], expire: int = None) -> int:
        for i in range(3):
            try:
                result = await self.write_conn.rpush(key, *values)
                await self._set_expire(key, expire)
                return result
            except Exception as e:
                if i < 2:
                    await asyncio.sleep(0.1)
                    await self._reconnect()
                else:
                    raise e

    async def lrem(self, key: str, count: int, value: str, expire: int = None) -> int:
        for i in range(3):
            try:
                result = await self.write_conn.lrem(key, count, value)
                await self._set_expire(key, expire)
                return result
            except Exception as e:
                if i < 2:
                    await asyncio.sleep(0.1)
                    await self._reconnect()
                else:
                    raise e

    async def hset(self, key: str, field: str, value: Union[str, int, float], expire: int = None) -> int:
        for i in range(3):
            try:
                result = await self.write_conn.hset(key, field, value)
                await self._set_expire(key, expire)
                return result
            except Exception as e:
                if i < 2:
                    await asyncio.sleep(0.1)
                    await self._reconnect()
                else:
                    raise e

    async def hdel(self, key: str, *fields: str, expire: int = None) -> int:
        for i in range(3):
            try:
                result = await self.write_conn.hdel(key, *fields)
                await self._set_expire(key, expire)
                return result
            except Exception as e:
                if i < 2:
                    await asyncio.sleep(0.1)
                    await self._reconnect()
                else:
                    raise e

    async def get_str(self, key: str) -> Union[str, None]:
        """Read a Redis value with retries and health checks."""
        await self._check_connection_health()
        
        for i in range(3):
            try:
                result = await asyncio.wait_for(
                    self.read_conn.get(key), 
                    timeout=2.0  # Two-second timeout.
                )
                return result
            except asyncio.TimeoutError:
                StandLogger.warn(f"Redis GET操作超时 (尝试 {i+1}/3): key={key}")
                if i < 2:
                    self._connection_healthy = False
                    await asyncio.sleep(0.1 * (i + 1))
                    await self._reconnect()
                else:
                    StandLogger.error(f"Redis GET操作最终超时: key={key}")
                    raise Exception(f"Redis GET timed out: {key}")
            except Exception as e:
                StandLogger.warn(f"Redis GET操作失败 (尝试 {i+1}/3): {str(e)}")
                if i < 2:
                    self._connection_healthy = False
                    await asyncio.sleep(0.1 * (i + 1))  # Incremental backoff.
                    await self._reconnect()
                else:
                    StandLogger.error(f"Redis GET操作最终失败: key={key}, error={str(e)}")
                    raise e

    async def delete_str(self, key: str | list[str]) -> bool:
        """
        Delete one or more Redis keys.

        Args:
            key: One key or a list of keys.
        Returns:
            Whether any key was deleted.
        """
        if isinstance(key, str):
            key = [key]
        for i in range(3):
            try:
                async with self.write_conn.pipeline(transaction=True) as pipe:
                    for k in key:
                        if await self.exists(k):
                            pipe.delete(k)
                    results = await pipe.execute()
                return any(result > 0 for result in results)
            except Exception as e:
                if i < 2:
                    await asyncio.sleep(0.1)
                    await self._reconnect()
                else:
                    raise e

    async def lrange(self, key: str, start: int = 0, end: int = -1) -> List:
        for i in range(3):
            try:
                return await self.read_conn.lrange(key, start, end)
            except Exception as e:
                if i < 2:
                    await asyncio.sleep(0.1)
                    await self._reconnect()
                else:
                    raise e

    async def hget(self, key: str, field: str) -> Union[str, None]:
        for i in range(3):
            try:
                return await self.read_conn.hget(key, field)
            except Exception as e:
                if i < 2:
                    await asyncio.sleep(0.1)
                    await self._reconnect()
                else:
                    raise e

    async def hgetall(self, key: str) -> Dict:
        for i in range(3):
            try:
                return await self.read_conn.hgetall(key)
            except Exception as e:
                if i < 2:
                    await asyncio.sleep(0.1)
                    await self._reconnect()
                else:
                    raise e

    async def exists(self, key: str) -> bool:
        for i in range(3):
            try:
                return await self.read_conn.exists(key) > 0
            except Exception as e:
                if i < 2:
                    await asyncio.sleep(0.1)
                    await self._reconnect()
                else:
                    raise e


async def get_redis_util():
    """
    Return the initialized redis_util instance.
    """
    global redis_util
    if redis_util is None:

        try:
            redis_util = await ConnectUtil.create()
            StandLogger.info("Redis连接工厂初始化成功")
        except Exception as e:
            StandLogger.error(f"Redis连接工厂初始化失败: {str(e)}")
            raise e
    return redis_util

class MyKafkaClient(object):
    def __init__(self, topic_name='tenant_a.dip.model_manager.quota_data'):
        self.producer = None
        self.consumer = None
        self.topic_name = topic_name
        self.admin_client = None
        self._init_admin_client()
        self._check_and_create_topic()

    def _init_admin_client(self):
        """Initialize the Kafka AdminClient."""
        self.admin_client = AdminClient({
            'bootstrap.servers': '{}:{}'.format(base_config.KAFKAHOST, base_config.KAFKAPORT),
            'security.protocol': 'sasl_plaintext',
            'enable.ssl.certificate.verification': 'false',
            'sasl.mechanism': 'PLAIN',
            'sasl.username': base_config.KAFKAUSER,
            'sasl.password': base_config.KAFKAPASS,
        })

    def _check_and_create_topic(self):
        """Create the Kafka topic when it does not exist."""
        try:
            # Use a 30-second metadata timeout because IPv6 DNS fallback can take 10 seconds in a pod.
            # libc may wait for an AAAA lookup to fail before falling back to an A record.
            # A 10-second timeout can fail _TRANSPORT and block service startup.
            # Allow enough time for IPv4 fallback before the startup probe terminates the pod.
            metadata = self.admin_client.list_topics(timeout=30)
            
            if self.topic_name not in metadata.topics:
                new_topic = NewTopic(
                    topic=self.topic_name,
                    num_partitions=3,  # Use three partitions.
                    replication_factor=1  # Use a replication factor of one.
                )
                
                fs = self.admin_client.create_topics([new_topic])
                for topic, f in fs.items():
                    try:
                        f.result()  # Wait for creation to complete.
                        StandLogger.info(f"Topic {topic} created successfully")
                    except Exception as e:
                        StandLogger.error(f"Failed to create topic {topic}: {e}")
            else:
                StandLogger.info(f"Topic {self.topic_name} already exists")
        except Exception as e:
            StandLogger.error(f"Error checking/creating topic: {e}")

    def connect_producer(self):
        """Connect the Kafka producer."""
        if self.producer is None:
            self.producer = Producer({
                'bootstrap.servers': '{}:{}'.format(base_config.KAFKAHOST, base_config.KAFKAPORT),
                'security.protocol': 'sasl_plaintext',
                'enable.ssl.certificate.verification': 'false',
                'sasl.mechanism': 'PLAIN',
                'sasl.username': base_config.KAFKAUSER,
                'sasl.password': base_config.KAFKAPASS,
                'acks': 1,  # Acknowledge after the leader receives the message.
                'queue.buffering.max.messages': 100000,  # Increase the queue buffer size.
                'queue.buffering.max.kbytes': 102400,   # Increase the queue buffer size in KB.
                'batch.num.messages': 1000,             # Maximum messages per batch.
                'linger.ms': 5                          # Batch linger time in milliseconds.
            })

    def connect_consumer(self, group_id='quota_data_group'):
        """Connect the Kafka consumer."""
        if self.consumer is None:
            self.consumer = Consumer({
                'bootstrap.servers': '{}:{}'.format(base_config.KAFKAHOST, base_config.KAFKAPORT),
                'security.protocol': 'sasl_plaintext',
                'enable.ssl.certificate.verification': 'false',
                'sasl.mechanism': 'PLAIN',
                'sasl.username': base_config.KAFKAUSER,
                'sasl.password': base_config.KAFKAPASS,
                'group.id': group_id,
                'auto.offset.reset': 'latest',  # Use latest or a new consumer group ID.
                'enable.auto.commit': True,
                'auto.commit.interval.ms': 1000
            })
            self.consumer.subscribe([self.topic_name])

    def produce_async(self, value, key=None, callback=None):
        """Send a Kafka message asynchronously."""
        if self.producer is None:
            self.connect_producer()
        
        self.producer.produce(
            self.topic_name, 
            key=key, 
            value=value, 
            callback=callback or self._delivery_callback
        )
        
        self.producer.poll(0)

    def _delivery_callback(self, err, msg):
        """Handle Kafka delivery results."""
        if err:
            StandLogger.error(f'Message delivery failed: {err}')
        else:
            StandLogger.info(f'Message delivered to {msg.topic()} [{msg.partition()}] at offset {msg.offset()}')

    def flush_producer(self, timeout=10):
        """Flush the producer queue."""
        if self.producer is not None:
            return self.producer.flush(timeout)
        return 0

    def consume_messages(self, timeout=1.0):
        """Consume messages from Kafka."""
        if self.consumer is None:
            self.connect_consumer()
        
        try:
            msg = self.consumer.poll(timeout)
            
            if msg is None:
                return None
            
            if msg.error():
                StandLogger.error(f'Consumer error: {msg.error()}')
                return None
            
            return {
                'key': msg.key(),
                'value': msg.value(),
                'topic': msg.topic(),
                'partition': msg.partition(),
                'offset': msg.offset()
            }
        except Exception as e:
            StandLogger.error(f'Error consuming message: {e}')
            return None

    def consume_batch(self, num_messages: int = 500, timeout: float = 1.0):
        """Consume a batch and return normalized records."""
        if self.consumer is None:
            self.connect_consumer()
        try:
            msgs = self.consumer.consume(num_messages=num_messages, timeout=timeout)
            result = []
            for msg in msgs or []:
                if msg is None:
                    continue
                if msg.error():
                    StandLogger.error(f'Consumer error: {msg.error()}')
                    continue
                result.append({
                    'key': msg.key(),
                    'value': msg.value(),
                    'topic': msg.topic(),
                    'partition': msg.partition(),
                    'offset': msg.offset()
                })
            return result
        except Exception as e:
            StandLogger.error(f'Error consuming batch messages: {e}')
            return []

    def close_producer(self):
        """Close the Kafka producer."""
        if self.producer is not None:
            self.flush_producer()
            self.producer = None

    def close_consumer(self):
        """Close the Kafka consumer."""
        if self.consumer is not None:
            self.consumer.close()
            self.consumer = None
redis_util = None

if resolve_metering_backend() == 'kafka':
    kafka_client = MyKafkaClient()
else:
    kafka_client = None
    StandLogger.info("计量后端为 redis，跳过 Kafka 客户端初始化")
