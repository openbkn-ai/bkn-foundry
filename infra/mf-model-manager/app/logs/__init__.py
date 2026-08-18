import logging
from logging import handlers

sys_log = logging.getLogger('user_manage')


def log_init():
    sys_log.setLevel(level=logging.DEBUG)
    formatter = logging.Formatter(
        'Process ID:%(process)d - '
        'Thread ID:%(thread)d- '
        'Log time:%(asctime)s - '
        'Log level:%(levelname)s - '
        'Message:%(message)s'
    )
    sys_log.handlers.clear()
    file_handler = handlers.TimedRotatingFileHandler('user_app_logs.log', encoding='utf-8', when='W6')
    file_handler.setLevel(level=logging.INFO)
    file_handler.setFormatter(formatter)
    sys_log.addHandler(file_handler)
