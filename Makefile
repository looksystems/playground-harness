.PHONY: proto proto-python proto-php proto-go clean-proto

PROTO_DIR := proto
PROTO_FILES := $(wildcard $(PROTO_DIR)/openshell/*.proto)
GO_MODULE := agent-harness/go

proto: proto-python proto-php proto-go
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

proto-go:
	@echo "Generating Go stubs..."
	protoc \
		--go_out=src/go/proto --go_opt=paths=source_relative \
		--go_opt=Mopenshell/openshell.proto=$(GO_MODULE)/proto/openshell \
		--go_opt=Mopenshell/sandbox.proto=$(GO_MODULE)/proto/openshell \
		--go_opt=Mopenshell/datamodel.proto=$(GO_MODULE)/proto/openshell \
		--go-grpc_out=src/go/proto --go-grpc_opt=paths=source_relative \
		--go-grpc_opt=Mopenshell/openshell.proto=$(GO_MODULE)/proto/openshell \
		--go-grpc_opt=Mopenshell/sandbox.proto=$(GO_MODULE)/proto/openshell \
		--go-grpc_opt=Mopenshell/datamodel.proto=$(GO_MODULE)/proto/openshell \
		-I$(PROTO_DIR) $(PROTO_FILES)

clean-proto:
	rm -rf src/python/generated/openshell/*_pb2*.py
	rm -rf src/python/generated/openshell/*_pb2*.pyi
	rm -rf src/php/Generated/OpenShell/*.php
	rm -rf src/go/proto/openshell/*.pb.go
