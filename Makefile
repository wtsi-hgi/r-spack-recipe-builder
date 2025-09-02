BINARY := py-package-uv-creator
PKG := ./cmd/py-package-uv-creator
OUT := $(BINARY)

.PHONY: build

build: 
	GO111MODULE=off go build -o $(OUT) $(PKG)




