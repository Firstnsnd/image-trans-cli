# Image Transfer CLI Tool

`image-trans-cli` is a command-line tool for managing Docker images. It supports pulling images from a source registry, tagging images, and pushing them to a target registry.

## Features

- Pull images from a source registry
- Tag images with a new repository
- Push images to a target registry

## Installation

1. Ensure that [Go] and [Docker] are installed on your system.

2. Clone this repository:

   ```bash
   git clone https://github.com/Firstnsnd/image-trans-cli.git
   cd image-trans-cli
   ```
3. Build the project:
   ```bash
   make build
   ```
   Alternatively, if you want to build for Windows:
   ```bash
   make build-windows
   ```
4. Copy the compiled binary to your PATH, or use it directly in the current directory.

## Usage
### Configuration File Format
   Create a YAML configuration file (e.g., config.yaml) with the following format:
   ```yaml
   images:
      - nginx:latest
      - redis:6
   target: my-registry.com
   ```
### Confirm Permissions
Before pushing images to the target registry, please ensure that you have sufficient permissions to perform the following actions:
1. Login to the target registry:
   ```bash
   docker login my-registry.com
   ```
2. Ensure that your account has permissions to push images.
3. If using CI/CD tools, make sure the related permissions are configured correctly.

### Command Line Usage
   Run the tool and specify the configuration file:
   ```bash
   ./image-trans-cli -c config.yaml
   ```
### Command Line Arguments
| Argument	     | Description                                    |
|---------------|------------------------------------------------|
| -c, --config	 | Path to the YAML configuration file (required) |
| -v, --verbose | 	Enable verbose output                         |
| --dry-run	    | Preview actions without executing them         |

### Examples
1. Process the images from the configuration file:
   ```bash
   ./image-trans-cli -c ./config.yaml
   ```
2. Enable verbose output:
   ```bash
   ./image-trans-cli -c ./config.yaml -v
   ```
3. Perform a dry run (preview actions):
   ```bash
   ./image-trans-cli -c ./config.yaml --dry-run
   ```
   
## Output Results
   After processing is complete, the program will display the results, including statistics of successful and failed image transfers.
## Contributing
   If you would like to contribute to this project, please create a new branch, make your changes, and submit a Pull Request.
## License
   This project is licensed under the MIT License. For more details, please see the LICENSE file.
