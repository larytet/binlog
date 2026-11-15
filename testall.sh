#!/bin/bash
# Runs unit tests with coverage and the benchmarks of every package.
# Try: go tool pprof profile-binlog.out ; go tool cover -html=coverage-binlog.out
cd "$(dirname "$0")"
set -e
for pkg in ./ast ./io .
do
	go test -v -cover -cpuprofile profile-binlog.out -bench=. -coverprofile=coverage-binlog.out "$pkg"
done
