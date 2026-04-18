FROM golang:1.26.2-alpine

# gcc + musl-dev: needed to build the host program, which imports "plugin"
# (stdlib plugin package uses dlopen via cgo). binutils-gold because Go's
# link driver invokes `gcc -fuse-ld=gold` when linking cgo binaries.
RUN apk add --no-cache gcc musl-dev binutils-gold file

# No default command — the harness invokes a one-shot shell script.
