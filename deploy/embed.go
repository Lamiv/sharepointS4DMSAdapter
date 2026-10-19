// Package deploy embeds the deployment templates that the zaks-drive command writes
// when it sets up a new installation. Real deployments contain .env and config.yaml,
// which are never embedded (they hold secrets and are git-ignored).
package deploy

import _ "embed"

// ComposeRelease runs a prebuilt image zaks-drive:<version>; it has no build section.
//
//go:embed docker-compose.release.yml
var ComposeRelease []byte

// DockerfileRelease builds the runtime image from a verified release binary.
//
//go:embed Dockerfile.release
var DockerfileRelease []byte

// ConfigExample is the commented configuration the setup wizard starts from.
//
//go:embed config.example.yaml
var ConfigExample []byte

// EnvExample documents the variables of .env.
//
//go:embed .env.example
var EnvExample []byte
