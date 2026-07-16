# Image Transfer CLI Tool

`image-trans-cli` is a command-line tool for transferring container images between OCI-compatible registries. **No Docker daemon or CLI required** — it communicates directly with registries over HTTPS.

## Features

- **Transfer images** between registries (pull → push)
- **Login** to any OCI-compatible registry, credentials saved to `~/.docker/config.json`
- **Compress mode** — save images as local `.tar.gz` files instead of pushing
- **Push tarball** — push a local `.tar` or `.tar.gz` to a remote registry
- **Mirror support** — use a registry mirror for Docker Hub (useful behind firewalls)
- **Retry** — automatic retry (3 attempts) on failure
- **Dry-run** — preview without executing

## Installation

1. Ensure [Go](https://go.dev/) is installed (1.22+).
2. Clone this repository:

   ```bash
   git clone https://github.com/Firstnsnd/image-trans-cli.git
   cd image-trans-cli
   ```
3. Build the project:

   ```bash
   make build
   ```
4. The compiled binary `image-trans-cli` is ready to use. No Docker installation needed.

## Usage

### Configuration File

Create a YAML configuration file (e.g., `config.yaml`):

```yaml
# optional: Docker Hub mirror (useful in regions where Docker Hub is blocked)
mirror: docker.m.daocloud.io

# optional: enable compression (save as .tar.gz instead of pushing to registry)
compress: true

# optional: output directory for compressed tarballs (default: current dir)
output: ./images

images:
  - nginx:latest
  - redis:alpine
target: my-registry.com
```

### Commands

#### Transfer Images

```bash
./image-trans-cli -c config.yaml
./image-trans-cli -c config.yaml -v           # verbose with download progress
./image-trans-cli -c config.yaml --dry-run    # preview only
```

#### Login

```bash
# Interactive (password hidden)
image-trans-cli login -u myuser

# With password (private registry)
image-trans-cli login my-registry.com -u admin -p mypassword

# From stdin (recommended for scripts)
echo "$PASSWORD" | image-trans-cli login my-registry.com -u admin --password-stdin
```

Credentials are saved to `~/.docker/config.json` and shared with Docker CLI.

#### Push Local Tarball

```bash
# Uncompressed tarball
image-trans-cli push image.tar my-registry.com/myapp:v1.0

# Compressed tarball (auto-detected by .tar.gz extension)
image-trans-cli push image.tar.gz my-registry.com/myapp:v1.0 -v
```

### Flags

| Flag | Description |
|------|-------------|
| `-c, --config` | Path to YAML config file (required for transfer) |
| `-v, --verbose` | Enable verbose output with download progress |
| `--dry-run` | Preview without executing |

### Examples

**Transfer with mirror (for users behind firewalls):**

```yaml
# config.yaml
mirror: docker.m.daocloud.io
images:
  - nginx:latest
  - redis:alpine
target: my-registry.com
```

```bash
./image-trans-cli -c config.yaml -v
```

**Save images locally without pushing:**

```yaml
# config.yaml
mirror: docker.m.daocloud.io
compress: true
output: ./output
images:
  - nginx:latest
  - python:3.11-slim
target: my-registry.com
```

```bash
./image-trans-cli -c config.yaml
# Output:
#   ./output/my-registry.com_nginx_latest.tar.gz
#   ./output/my-registry.com_python_3.11-slim.tar.gz
```

**Push saved tarballs later:**

```bash
image-trans-cli push ./output/my-registry.com_nginx_latest.tar.gz my-registry.com/nginx:latest
```

## Output Results

After processing, the program displays a summary of successful and failed transfers.

## Contributing

If you would like to contribute, please create a new branch, make your changes, and submit a Pull Request.

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file.
