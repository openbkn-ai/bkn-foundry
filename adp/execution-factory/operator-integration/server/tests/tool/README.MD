# Operator operation tool.

## Tool introduction.

This tool is used to register, update, query, delete, publish, offline and other operations on operators.

## Environment preparation.

```bash
# Python 3.10+
python3 -m pip install requests==2.31.0 pyyaml==6.0.1 argparse==1.4.0
```

## Usage.

This tool supports two usage methods: **Run Python script directly** or **Compile it into a binary file and run it**.

### Method 1: Use Python script directly (recommended)

```bash
# View help.
python3 operator_client.py -h

# General command format.
python3 operator_client.py --config=<configuration file path> <command>.
```

### Method 2: Compile into binary file.

To generate a stand-alone executable file, you can use PyInstaller to compile:

```bash
# Install PyInstaller.
python3 -m pip install pyinstaller==6.2.0

#Compile into binary file (output to dist/ directory)
pyinstaller --onefile --name operator-cli operator_client.py

# Use compiled binaries.
./dist/operator-cli --config=<configuration file path> <command>.
```

## Configuration instructions.

> 1. The tool requires a configuration file. The configuration file format is YAML. For an example, see [config.yaml](./config.yaml)
> 2. `token` (user login credentials) needs to be set in the configuration file for authentication.
> 3. `base_url` (platform address) needs to be set in the configuration file for request.
> 4. Please see the configuration file for details of other required parameters.
> 5. For API details, please see the document: [DIP Operator Service Public Interface Protocol](../../../docs/apis/api_public/operator.html)

## Command list.

```bash
# Register operator.
python3 operator_client.py --config=config.yaml register

#Update operator.
python3 operator_client.py --config=config.yaml update

# Query list.
python3 operator_client.py --config=config.yaml list

# Query details.
python3 operator_client.py --config=config.yaml detail

# Delete operator.
python3 operator_client.py --config=config.yaml delete

# Release operator.
python3 operator_client.py --config=config.yaml publish

# Offline operator.
python3 operator_client.py --config=config.yaml offline
```
