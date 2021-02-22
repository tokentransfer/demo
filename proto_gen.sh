cd core/pb/protos
protoc --go_out=plugins=grpc:.. *.proto

cd ../../..