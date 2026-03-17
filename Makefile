.PHONY: proto proto-python proto-php clean-proto

PROTO_DIR := proto
PROTO_FILES := $(wildcard $(PROTO_DIR)/openshell/*.proto)

proto: proto-python proto-php
	@echo "Proto codegen complete."

proto-python:
	@echo "Generating Python stubs..."
	python -m grpc_tools.protoc \
		--python_out=src/python/generated \
		--grpc_python_out=src/python/generated \
		--pyi_out=src/python/generated \
		-I$(PROTO_DIR) $(PROTO_FILES)

proto-php:
	@echo "Generating PHP stubs..."
	protoc \
		--php_out=src/php/Generated \
		--grpc_out=src/php/Generated \
		--plugin=protoc-gen-grpc=$$(which grpc_php_plugin) \
		-I$(PROTO_DIR) $(PROTO_FILES)

clean-proto:
	rm -rf src/python/generated/openshell/*_pb2*.py
	rm -rf src/python/generated/openshell/*_pb2*.pyi
	rm -rf src/php/Generated/OpenShell/*.php
