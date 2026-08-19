# Operator CLI Tool v1.0
# (c) 2024 AISHU DevOps Team
import argparse
import requests
import json
import os
import yaml


# OperatorClient: encapsulates Operator API calls.
class OperatorClient:
    def __init__(self, base_url, token):
        # Read configuration file.
        self.BASE_URL = base_url
        self.headers = {
            "Authorization": token,
            "Content-Type": "application/json"
        }
        self.url = f"{self.BASE_URL}/api/agent-operator-integration/v1"

    def _send_request(self, method, endpoint, params=None, data=None):
        url = f"{self.url}{endpoint}"
        try:
            response = requests.request(
                method=method,
                url=url,
                headers=self.headers,
                params=params,
                json=data,
                verify=False
            )
            response.raise_for_status()
            # Add verification, if the response is empty, return status code and response text.
            if not response.text.strip():
                return {"code": response.status_code}
            return json.dumps(
                    response.json(),
                    ensure_ascii=False,
                    indent=4,
                    default=str)

        except requests.exceptions.HTTPError as err:
            print(f"HTTP Error: {err}")
            print(f"Response: {response.text}")
        except Exception as err:
            print(f"Error: {err}")

    def register_operator(self, data):
        endpoint = "/operator/register"
        return self._send_request('POST', endpoint, data=data)

    def update_operator(self, data):
        endpoint = "/operator/info/update"
        return self._send_request('POST', endpoint, data=data)

    def list_operators(self, **kwargs):
        endpoint = "/operator/info/list"
        return self._send_request('GET', endpoint, params=kwargs)

    def delete_operator(self, operator_id, version):
        endpoint = "/operator/delete"
        data = []
        data.append({
            "operator_id": operator_id,
            "version": version
        })
        return self._send_request('DELETE', endpoint, data=data)

    def get_operator_info(self, operator_id, version=None):
        endpoint = f"/operator/info/{operator_id}"
        params = {"version": version} if version else None
        return self._send_request('GET', endpoint, params=params)

    def get_categories(self):
        endpoint = "/operator/category"
        return self._send_request('GET', endpoint)

    def update_status(self, operator_id, version, target_status):
        endpoint = "/operator/status"
        data = []
        data.append({
            "operator_id": operator_id,
            "version": version,
            "status": target_status
        })
        return self._send_request('POST', endpoint, data=data)


# OperatorOperation operator operation class.
class OperatorOperation:
    def __init__(self, client, config):
        self.client = client
        self.config = config
        # Initialize operating parameters.
        self._init_operation_configs()

    def _init_operation_configs(self):
        """Load each operating parameter from the configuration file."""
        self.register_config = self.config.get('register', {})
        self.update_config = self.config.get('update', {})
        self.delete_config = self.config.get('delete', {})
        self.list_config = self.config.get('list', {}).get('query', {})
        self.detail_config = self.config.get('detail', {})
        self.publish_config = self.config.get('publish', {})
        self.offline_config = self.config.get('offline', {})

    def register_operator(self):
        """Register operator (using configuration file parameters)"""
        payload = {
            "operator_metadata_type": self.register_config.get('metadata_type'),
            "direct_publish": self.register_config.get('direct-publish'),
            "operator_info": self.register_config.get('operator_info'),
            "operator_execute_control":
                self.register_config.get('operator_execute_control'),
            "extend_info": self.register_config.get('extend_info')
        }
        with open(self.register_config['metadata_path'], 'r',
                  encoding='utf-8') as register_file:
            payload['data'] = register_file.read()

        return self.client.register_operator(payload)

    def update_operator(self):
        """Update operator information."""
        # Dynamic parameters take precedence over configuration file parameters.
        payload = {
            "operator_id": self.update_config.get('operator_id'),
            "version": self.update_config.get('version'),
            "operator_metadata_type": self.update_config.get('metadata_type'),
            "direct_publish": self.update_config.get('direct-publish'),
            "operator_info": self.update_config.get('operator_info'),
            "operator_execute_control":
                self.update_config.get('operator_execute_control'),
            "extend_info": self.update_config.get('extend_info')
        }

        with open(self.update_config['metadata_path'], 'r', encoding='utf-8') as update_file:
            payload['data'] = update_file.read()

        return self.client.update_operator(payload)

    def list_operators(self):
        """Query operator list."""
        query_params = {k: v for k, v in self.list_config.items() if v is not None}
        return self.client.list_operators(**query_params)

    def detail_operator(self):
        """Query operator details."""
        operator_id = self.detail_config.get('operator_id')
        version = self.detail_config.get('version')
        return self.client.get_operator_info(operator_id, version)

    def publish_operator(self):
        """Release operator."""
        operator_id = self.publish_config.get('operator_id')
        version = self.publish_config.get('version')
        return self.client.update_status(operator_id, version, 'published')

    def offline_operator(self):
        """offline operator."""
        operator_id = self.offline_config.get('operator_id')
        version = self.offline_config.get('version')
        return self.client.update_status(operator_id, version, 'offline')

    def delete_operator(self):
        """delete operator."""
        operator_id = self.delete_config.get('operator_id')
        version = self.delete_config.get('version')
        return self.client.delete_operator(operator_id, version)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description='Operator Management CLI')
    # If you use a configuration file, you can define base_url and token in the configuration file.
    parser.add_argument('--config', default="./config.yaml", help='Path to config file')
    subparsers = parser.add_subparsers(dest='command')
    # Register command
    register_parser = subparsers.add_parser('register')
    # Update command
    update_parser = subparsers.add_parser('update')
    # List command
    list_parser = subparsers.add_parser('list')
    # Detail command
    detail_parser = subparsers.add_parser('detail')
    # Publish command
    publish_parser = subparsers.add_parser('publish')
    # Offline command
    offline_parser = subparsers.add_parser('offline')
    # Delete command
    delete_parser = subparsers.add_parser('delete')
    args = parser.parse_args()
    config = {}
    if args.config:  # Prioritize using the configuration file specified by --config.
        try:
            with open(args.config, 'r', encoding='utf-8') as f:
                config = yaml.safe_load(f)
        except FileNotFoundError:
            print(f"Error: 配置文件 {args.config} 不存在")
            exit(1)
    elif os.path.exists('config.yaml'):  # Secondly check the default configuration file.
        with open('config.yaml', 'r', encoding='utf-8') as f:
            config = yaml.safe_load(f)

    # Override parameters with values from config file.
    if 'base_url' in config:
        args.base_url = config['base_url']
    if 'token' in config:
        args.token = config.get('token')

    # Final verification of whether the token is configured.
    if not args.token:
        parser.error("必须通过以下方式之一提供token: \n1. --token 参数\n2. 配置文件(--config指定)\n3. 当前目录的config.json")

    client = OperatorClient(args.base_url, args.token)
    operation = OperatorOperation(client, config)

    if args.command == 'register':
        print(operation.register_operator())
    elif args.command == 'update':
        print(operation.update_operator())
    elif args.command == 'list':
        print(operation.list_operators())
    elif args.command == 'detail':
        print(operation.detail_operator())
    elif args.command == 'publish':
        print(operation.publish_operator())
    elif args.command == 'offline':
        print(operation.offline_operator())
    elif args.command == 'delete':
        print(operation.delete_operator())