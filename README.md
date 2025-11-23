# nview

[![CI](https://github.com/blinklabs-io/nview/actions/workflows/go-test.yml/badge.svg)](https://github.com/blinklabs-io/nview/actions/workflows/go-test.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/blinklabs-io/nview)](https://goreportcard.com/report/github.com/blinklabs-io/nview)
[![Go Version](https://img.shields.io/badge/go-1.25+-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](https://github.com/blinklabs-io/nview/blob/main/LICENSE)

nview is a local monitoring tool for Cardano nodes, supporting multiple
implementations including cardano-node, Dingo, and Amaru. It's designed to
complement remote monitoring tools by providing a local command-line view of
running nodes. The TUI (terminal user interface) is built to fit most screens
and provides real-time metrics and status information.

<div align="center">
    <img src="./nview-20240208.png" alt="nview screenshot" width="640">
</div>

## Design and functionality

The goal with nview is to provide an alternative to the Guild LiveView
(gLiveView.sh) shell scripts which is shipped as a single binary with all of
the functionality included natively. This allows the tool to be more portable
to non-Linux systems by using Go standard library functionality.

The design is more in line with a 12-factor application, with no config
files or other on-disk requirements. Functionality is controlled via
environment variables within the application's context. This prevents the
application from being a drop-in replacement for gLiveView for Cardano Node
administrators, but unlocks the ability to add functionality beyond that
easily attainable with a shell script.

### Multi-Implementation Support

nview automatically detects and supports multiple Cardano node implementations:

- **cardano-node**: The official Cardano node implementation
- **Dingo**: A Cardano node implementation focused on performance
- **Amaru**: Another Cardano node implementation with specific optimizations

The tool automatically detects the running node type based on Prometheus metrics
and adjusts its display accordingly. No configuration changes are needed - just
run nview against any supported Cardano node.

## Usage

Running nview against a running Cardano Node will work out of the box with a
default Cardano Node configuration, which exposes metrics in Prometheus data
format on a specific port. nview automatically detects the node implementation
(cardano-node, Dingo, or Amaru) and adjusts its display accordingly.

```bash
./nview
```

Or, from source:
```bash
go run .
```

### Configuration

Configuration can be controlled by either a configuration file or environment
variables. In cases where both are present, the environment variable takes
precedence.

#### Configuration (env)

The following environment variables control the behavior of the application.

- `NODE_NAME` - Changes the name displayed by nview, default is "Cardano
  Node", maximum 19 characters
- `NETWORK` - Short-cut environment variable to use a default configuration
  for the given known named network. Overrides `CARDANO_NETWORK`, default ""
- `CARDANO_NETWORK` - Named network configured on the Cardano Node, default
  is "mainnet"
- `CARDANO_NODE_BINARY` - Specifies the node binary type
  (`cardano-node`, `dingo`, or `amaru`). If not set, nview will attempt to
  auto-detect based on metrics
- `PROM_HOST` - Sets the host address used to fetch Prometheus metrics from a
  Cardano Node, default is "127.0.0.1"
- `PROM_PORT` - Sets the host port used to fetch Prometheus metrics from a
  Cardano Node, default is 12798
- `PROM_TIMEOUT` - Sets the maximum number of seconds to wait for response
  when polling a Cardano Node for Prometheus metrics, default is 3

#### Configuration (YAML)

To use a configuration file, run `nview` with a command line flag to set the
file to load as a configuration.

```bash
./nview -config /path/to/config.yml
```

config.yaml:
```
app:
  nodeName: Cardano Node
  network:
node:
  binary: cardano-node  # or 'dingo' or 'amaru'
  network: mainnet
  port: 3001
prometheus:
  host: 127.0.0.1
  port: 12798
  timeout: 3
```

An example configuration is provided at `config.yaml.example`.

## Examples

### Running with Dingo Node

```bash
# Auto-detection (recommended)
./nview

# Or explicitly set
CARDANO_NODE_BINARY=dingo ./nview
```

### Running with Amaru Node

```bash
# Auto-detection (recommended)
./nview

# Or explicitly set
CARDANO_NODE_BINARY=amaru ./nview
```

### Custom Prometheus Port

```bash
PROM_PORT=9090 ./nview
```

### Remote Node Monitoring

```bash
PROM_HOST=192.168.1.100 PROM_PORT=12798 ./nview
```

## Troubleshooting

### Node Not Detected

If nview shows "Cardano Node" instead of the expected implementation:

1. Ensure your node is running and exposing Prometheus metrics
2. Check that the correct `PROM_HOST` and `PROM_PORT` are set
3. For manual override, set `CARDANO_NODE_BINARY` environment variable

### Connection Issues

- Verify Prometheus metrics are accessible: `curl http://127.0.0.1:12798/metrics`
- Check firewall settings if monitoring remotely
- Increase timeout if on slow networks: `PROM_TIMEOUT=10 ./nview`

### Display Issues

- Ensure terminal supports Unicode characters
- Check terminal width (minimum 112 columns recommended)
- For color issues, ensure terminal supports 256 colors

## GeoLocation

We embed free GeoLite2 city data created by MaxMind, available
from https://www.maxmind.com and licensed under CC BY-SA 4.0
<https://creativecommons.org/licenses/by-sa/4.0/>
