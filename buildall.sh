#!/bin/bash
cd "$(dirname "$0")"
set -e
go build ./...
go vet ./ast ./io
