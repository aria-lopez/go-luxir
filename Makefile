# Go import path of the generated package. We pick it ourselves because Luxir's
# .proto files have no `go_package` option.
MOD := github.com/aria-lopez/go-luxir/luxirpb

# proto :: regenerate luxirpb/ from protos/ (Luxir v0.1.0)
#
# -I protos
#     where protoc looks for .proto files and their imports
#     (luxir.proto does `import "luxir_types.proto"`)
# --go_out / --go_opt=paths=source_relative
#     protoc-gen-go: writes the message structs, straight into luxirpb/
# --go_opt=M<file>=$(MOD)
#     "the Go package for this .proto file is this import path"; required
#     because the files don't say, and both share one proto package (luxir)
# --go-grpc_out / --go-grpc_opt
#     protoc-gen-go-grpc: writes the client/server stubs, same mappings
.PHONY: proto
proto:
	mkdir -p luxirpb
	protoc -I protos \
		--go_out=luxirpb      --go_opt=paths=source_relative \
		--go_opt=Mluxir_types.proto=$(MOD) --go_opt=Mluxir.proto=$(MOD) \
		--go-grpc_out=luxirpb --go-grpc_opt=paths=source_relative \
		--go-grpc_opt=Mluxir_types.proto=$(MOD) --go-grpc_opt=Mluxir.proto=$(MOD) \
		protos/luxir_types.proto protos/luxir.proto
