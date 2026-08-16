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
import concurrent.futures
import threading
from queue import Queue, Empty
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
                # Validate the connection.
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
                # Validate the connection.
                await redis_con.ping()
                return redis_con
            except Exception as e:
                StandLogger.error(f"Redis连接失败 - master-slave模式: host={self.redis_ip}, port={self.redis_port}, error={str(e)}")
                raise Exception(f"connect redis error:{str(e)}")
        else:
            # Standalone mode.
            try:
                pool = redis_async.ConnectionPool(host=self.redis_ip, port=self.redis_port, db=db,
                                                  password=self.redis_passwd, username=self.redis_user if self.redis_user else None)
                redis_con = redis_async.StrictRedis(connection_pool=pool)
                # Validate the connection.
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
                # Validate the connection.
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
                # Validate the connection.
                await redis_con.ping()
                return redis_con
            except Exception as e:
                StandLogger.error(f"Redis连接失败 - {model}模式: host={self.redis_ip}, port={self.redis_port}, error={str(e)}")
                raise Exception(f"connect redis error:{str(e)}")
        else:
            # Standalone mode.
            try:
                pool = redis_async.ConnectionPool(host=self.redis_ip, port=self.redis_port, db=db,
                                                  password=self.redis_passwd, username=self.redis_user if self.redis_user else None)
                redis_con = redis_async.StrictRedis(connection_pool=pool)
                # Validate the connection.
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
                    raise Exception("Redis connection creation failed")
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
            
            if redis_client.redis_cluster_mode == "sentinel":
                # Use Sentinel-managed connections.
                await self._init_sentinel_connection_pools(redis_client)
            elif redis_client.redis_cluster_mode == "master-slave":
                # Use separate primary and replica connections.
                await self._init_master_slave_connection_pools(redis_client)
            else:
                # Use a standalone connection.
                await self._init_default_connection_pools(redis_client)
            
            # Warm the pools by establishing initial connections.
            await self._warmup_connections()
            
            StandLogger.info("Redis高性能连接池初始化成功")
            
        except Exception as e:
            StandLogger.error(f"Redis连接池初始化失败: {str(e)}")
            raise e

    async def _init_sentinel_connection_pools(self, redis_client):
        """Initialize Sentinel-mode connections."""
        try:
            # Create the Sentinel client.
            sentinel = Sentinel_async([(redis_client.redis_ip, redis_client.redis_port)], 
                                    password=redis_client.redis_sentinel_password,
                                    sentinel_kwargs={
                                        "password": redis_client.redis_sentinel_password,
                                        "username": redis_client.redis_sentinel_user
                                    })
            
            # Resolve the primary for writes.
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
            
            # Resolve a replica for reads.
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
        """Initialize primary/replica connection pools."""
        try:
            # Create the read pool.
            self.__class__._read_pool = redis_async.ConnectionPool(
                host=redis_client.redis_read_ip,
                port=redis_client.redis_read_port,
                db=self.db,
                password=redis_client.redis_read_passwd,
                max_connections=50,  # Maximum connections.
                retry_on_timeout=True,
                socket_keepalive=True,
                socket_connect_timeout=3,  # Connection timeout.
                socket_timeout=2,  # Operation timeout.
                health_check_interval=30,  # Health-check interval.
                decode_responses=False  # Retain bytes to avoid decode overhead.
            )
            
            # Create the write pool.
            self.__class__._write_pool = redis_async.ConnectionPool(
                host=redis_client.redis_write_ip,
                port=redis_client.redis_write_port,
                db=self.db,
                password=redis_client.redis_write_passwd,
                max_connections=30,  # Writes need fewer connections.
                retry_on_timeout=True,
                socket_keepalive=True,
                socket_connect_timeout=3,  # Connection timeout.
                socket_timeout=2,  # Operation timeout.
                health_check_interval=30,
                decode_responses=False
            )
            
            # Create Redis client instances.
            self.read_conn = redis_async.StrictRedis(connection_pool=self.__class__._read_pool)
            self.write_conn = redis_async.StrictRedis(connection_pool=self.__class__._write_pool)
            
            StandLogger.info("主从模式Redis连接池初始化成功")
            
        except Exception as e:
            StandLogger.error(f"主从模式Redis连接池初始化失败: {str(e)}")
            raise e

    async def _init_default_connection_pools(self, redis_client):
        """Initialize a standalone Redis connection pool."""
        try:
            # Create the standalone pool.
            self.__class__._read_pool = redis_async.ConnectionPool(
                host=redis_client.redis_ip,
                port=redis_client.redis_port,
                db=self.db,
                password=redis_client.redis_passwd,
                max_connections=50,  # Maximum connections.
                retry_on_timeout=True,
                socket_keepalive=True,
                socket_connect_timeout=3,  # Connection timeout.
                socket_timeout=2,  # Operation timeout.
                health_check_interval=30,  # Health-check interval.
                decode_responses=False  # Retain bytes to avoid decode overhead.
            )
            
            # Use the same client and pool for reads and writes.
            self.read_conn = redis_async.StrictRedis(connection_pool=self.__class__._read_pool)
            self.write_conn = redis_async.StrictRedis(connection_pool=self.__class__._read_pool)
            
            StandLogger.info("默认模式Redis连接池初始化成功")
            
        except Exception as e:
            StandLogger.error(f"默认模式Redis连接池初始化失败: {str(e)}")
            raise e

    async def _warmup_connections(self):
        """Warm the pool by establishing initial connections."""
        try:
            redis_client = self.__class__._redis_client
            
            if redis_client.redis_cluster_mode == "sentinel":
                # Ping Sentinel-managed clients directly.
                await self.read_conn.ping()
                await self.write_conn.ping()
            else:
                # Warm primary/replica and standalone pools.
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
        # Check at most once every 30 seconds.
        if current_time - self._last_health_check > 30:
            try:
                await self.read_conn.ping()
                await self.write_conn.ping()
                self._connection_healthy = True
                self._last_health_check = current_time
            except Exception as e:
                StandLogger.warn(f"Redis连接健康检查失败: {str(e)}")
                self._connection_healthy = False
                # Let the calling operation decide whether to reconnect.
        return self._connection_healthy

    async def _reconnect(self):
        """Re-establish Redis connections when required."""
        if self._connection_healthy:
            return
            
        StandLogger.info("开始重新建立Redis连接...")
        old_read_conn = self.read_conn
        old_write_conn = self.write_conn
        
        try:
            # Initialize replacement pools.
            await self._init_connection_pools()
            self._connection_healthy = True
            StandLogger.info("Redis连接重建成功")
            
            # Close old clients after the replacement succeeds.
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
            # Restore the old clients if reconnection fails.
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
                    import time
                    await asyncio.sleep(0.1)
                    await self._reconnect()
                else:
                    raise e

    async def set_if_absent(self, key: str, value: Union[str, int, float] = 1, expire: int | None = None) -> bool:
        """Set a key with SETNX semantics and an optional expiration."""
        for i in range(3):
            try:
                # Redis-py: set(name, value, ex=None, px=None, nx=False, xx=False)
                result = await self.write_conn.set(key, value, ex=expire, nx=True)
                return bool(result)
            except Exception as e:
                if i < 2:
                    import time
                    await asyncio.sleep(0.1)
                    await self._reconnect()
                else:
                    raise e

    async def _set_expire(self, key: str, expire: int = None) -> bool:
        """Set a key expiration internally."""
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
                    import time
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
                    import time
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
                    import time
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
                    import time
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
                    import time
                    await asyncio.sleep(0.1)
                    await self._reconnect()
                else:
                    raise e

    async def get_str(self, key: str) -> Union[str, None]:
        """Read a Redis value with retries and connection health checks."""
        # Check connection health before reading.
        await self._check_connection_health()
        
        for i in range(3):
            try:
                # Use a short deadline to avoid blocking callers.
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
                    raise Exception(f"Redis GET operation timed out: {key}")
            except Exception as e:
                StandLogger.warn(f"Redis GET操作失败 (尝试 {i+1}/3): {str(e)}")
                if i < 2:
                    # Mark the connection unhealthy.
                    self._connection_healthy = False
                    await asyncio.sleep(0.1 * (i + 1))  # Incremental backoff.
                    await self._reconnect()
                else:
                    StandLogger.error(f"Redis GET操作最终失败: key={key}, error={str(e)}")
                    raise e

    async def delete_str(self, key: str | list[str]) -> bool:
        """
        Delete one or more Redis keys.
        :param key: one key or a list of keys
        :return: whether any key was deleted
        """
        if isinstance(key, str):
            key = [key]
        for i in range(3):
            try:
                # Delete multiple keys through a pipeline.
                async with self.write_conn.pipeline(transaction=True) as pipe:
                    for k in key:
                        if await self.exists(k):
                            pipe.delete(k)
                    results = await pipe.execute()
                # Report success when at least one key was deleted.
                return any(result > 0 for result in results)
            except Exception as e:
                if i < 2:
                    import time
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
                    import time
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
                    import time
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
                    import time
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
                    import time
                    await asyncio.sleep(0.1)
                    await self._reconnect()
                else:
                    raise e


async def get_redis_util():
    """
    Return the initialized Redis utility singleton.
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
        
        # Asynchronous producer state.
        self._message_queue = Queue(maxsize=10000)  # Bound memory use.
        self._producer_thread = None
        self._shutdown_event = threading.Event()
        self._thread_pool = concurrent.futures.ThreadPoolExecutor(max_workers=2, thread_name_prefix="kafka-producer")
        self._is_async_running = False
        
        # Ensure the topic exists during initialization.
        self._init_admin_client()
        self._check_and_create_topic()
        
        # Start the asynchronous producer thread.
        self._start_async_producer()

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
            # Read cluster metadata.
            metadata = self.admin_client.list_topics(timeout=10)
            
            # Check whether the topic already exists.
            if self.topic_name not in metadata.topics:
                # Define the topic.
                new_topic = NewTopic(
                    topic=self.topic_name,
                    num_partitions=3,  # Three partitions.
                    replication_factor=1  # One replica.
                )
                
                # Submit topic creation.
                fs = self.admin_client.create_topics([new_topic])
                for topic, f in fs.items():
                    try:
                        f.result()  # Wait for completion.
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
                'acks': 1,  # Confirm receipt by the leader partition.
                'queue.buffering.max.messages': 100000,  # Buffered message limit.
                'queue.buffering.max.kbytes': 102400,   # Buffer size in KiB.
                'batch.num.messages': 1000,             # Messages per batch.
                'linger.ms': 5                          # Batch delay in milliseconds.
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
                'auto.offset.reset': 'latest',  # Start at the latest offset for a new group.
                'enable.auto.commit': True,
                'auto.commit.interval.ms': 1000
            })
            # Subscribe to the topic.
            self.consumer.subscribe([self.topic_name])

    def _start_async_producer(self):
        """Start the asynchronous producer thread."""
        if not self._is_async_running:
            self._producer_thread = threading.Thread(
                target=self._async_producer_worker,
                name="kafka-async-producer",
                daemon=True
            )
            self._producer_thread.start()
            self._is_async_running = True
            StandLogger.info("Kafka异步生产者线程已启动")

    def _async_producer_worker(self):
        """Run the asynchronous producer worker."""
        # Initialize the producer in its worker thread.
        self.connect_producer()
        
        while not self._shutdown_event.is_set():
            try:
                # Poll the queue with a one-second timeout.
                try:
                    message_data = self._message_queue.get(timeout=1.0)
                except Empty:
                    continue
                
                # Send the message.
                self.producer.produce(
                    self.topic_name,
                    key=message_data.get('key'),
                    value=message_data.get('value'),
                    callback=message_data.get('callback', self._delivery_callback)
                )
                
                # Poll delivery callbacks in batches.
                self.producer.poll(0.1)
                
                # Mark the queued task complete.
                self._message_queue.task_done()
                
            except Exception as e:
                StandLogger.error(f"异步生产者工作线程错误: {e}")
                time.sleep(0.1)  # Brief delay after an error.

    def produce_async(self, value, key=None, callback=None):
        """Queue a Kafka message without blocking."""
        try:
            # Prepare the queued message.
            message_data = {
                'value': value,
                'key': key,
                'callback': callback
            }
            
            # Drop and warn rather than block when the queue is full.
            try:
                self._message_queue.put_nowait(message_data)
            except:
                StandLogger.warn(f"Kafka消息队列已满，丢弃消息: key={key}")
                return False
                
            return True
            
        except Exception as e:
            StandLogger.error(f"异步发送Kafka消息失败: {e}")
            return False

    def produce_async_with_future(self, value, key=None):
        """Queue a message and return a Future for its delivery result."""
        future = concurrent.futures.Future()
        
        def delivery_callback(err, msg):
            if err:
                future.set_exception(Exception(f"Kafka message delivery failed: {err}"))
            else:
                future.set_result({
                    'topic': msg.topic(),
                    'partition': msg.partition(),
                    'offset': msg.offset()
                })
        
        success = self.produce_async(value, key, delivery_callback)
        if not success:
            future.set_exception(Exception("Kafka message queue is full"))
        
        return future

    def _delivery_callback(self, err, msg):
        """Handle Kafka delivery completion."""
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
        """Consume one message from Kafka."""
        if self.consumer is None:
            self.connect_consumer()
        
        try:
            # Poll for one message.
            msg = self.consumer.poll(timeout)
            
            if msg is None:
                return None
            
            if msg.error():
                StandLogger.error(f'Consumer error: {msg.error()}')
                return None
            
            # Return the decoded message content.
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
        """Close the producer connection."""
        if self.producer is not None:
            self.flush_producer()
            self.producer = None

    def close_consumer(self):
        """Close the consumer connection."""
        if self.consumer is not None:
            self.consumer.close()
            self.consumer = None

    def shutdown_async_producer(self):
        """Shut down the asynchronous producer gracefully."""
        if self._is_async_running:
            StandLogger.info("正在关闭Kafka异步生产者...")
            self._shutdown_event.set()
            
            # Wait for queued messages to finish.
            self._message_queue.join()
            
            # Wait for the worker thread to stop.
            if self._producer_thread and self._producer_thread.is_alive():
                self._producer_thread.join(timeout=5)
            
            # Shut down the thread pool.
            self._thread_pool.shutdown(wait=True)
            
            # Flush the producer once more.
            if self.producer is not None:
                self.flush_producer()
                self.producer = None
            
            self._is_async_running = False
            StandLogger.info("Kafka异步生产者已关闭")

    def get_queue_status(self):
        """Return producer queue status."""
        return {
            'queue_size': self._message_queue.qsize(),
            'queue_maxsize': self._message_queue.maxsize,
            'is_async_running': self._is_async_running,
            'thread_alive': self._producer_thread.is_alive() if self._producer_thread else False
        }

    def get_redis_pool_status(self):
        """Return Redis pool status."""
        if not self._instance:
            return {'error': 'Redis connection pool is not initialized'}
        
        status = {
            'connection_healthy': self._instance._connection_healthy,
            'last_health_check': self._instance._last_health_check,
            'read_pool': {},
            'write_pool': {}
        }
        
        if self._instance.__class__._read_pool:
            status['read_pool'] = {
                'max_connections': self._instance.__class__._read_pool.max_connections,
                'created_connections': len(self._instance.__class__._read_pool._created_connections),
                'available_connections': len(self._instance.__class__._read_pool._available_connections),
                'in_use_connections': len(self._instance.__class__._read_pool._in_use_connections)
            }
        
        if self._instance.__class__._write_pool:
            status['write_pool'] = {
                'max_connections': self._instance.__class__._write_pool.max_connections,
                'created_connections': len(self._instance.__class__._write_pool._created_connections),
                'available_connections': len(self._instance.__class__._write_pool._available_connections),
                'in_use_connections': len(self._instance.__class__._write_pool._in_use_connections)
            }
        
        return status

# Process-wide Redis utility instance.
redis_util = None

# Instantiate Kafka only for the Kafka metering backend. Its constructor connects
# to a broker and creates the topic; the Redis path lives in metering_producer.py.
if resolve_metering_backend() == 'kafka':
    kafka_client = MyKafkaClient()
else:
    kafka_client = None
    StandLogger.info("计量后端为 redis，跳过 Kafka 客户端初始化")
