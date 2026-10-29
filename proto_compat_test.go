package libratrack_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"strconv"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
	pb "libratrack/gen/inventory"
)

// The original generated descriptor in main is the compatibility baseline.
// Comparing descriptors also checks names/types used by Go and JSON clients.
func TestOriginalSchemaCompatibility(t *testing.T) {
	source, err := exec.Command("git", "show", "main:gen/inventory/inventory.pb.go").Output()
	if err != nil {
		source, err = exec.Command("git", "show", "origin/main:gen/inventory/inventory.pb.go").Output()
	}
	if err != nil {
		t.Fatalf("main baseline unavailable (fetch main first): %v", err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "inventory.pb.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	var encoded []byte
	ast.Inspect(file, func(node ast.Node) bool {
		value, ok := node.(*ast.ValueSpec)
		if !ok || len(value.Names) != 1 || value.Names[0].Name != "file_inventory_proto_rawDesc" {
			return true
		}
		literal, ok := value.Values[0].(*ast.CompositeLit)
		if !ok {
			t.Fatal("baseline descriptor is not a byte literal")
		}
		for _, element := range literal.Elts {
			integer, ok := element.(*ast.BasicLit)
			if !ok {
				t.Fatal("unexpected descriptor element")
			}
			number, err := strconv.ParseUint(integer.Value, 0, 8)
			if err != nil {
				t.Fatal(err)
			}
			encoded = append(encoded, byte(number))
		}
		return false
	})
	if len(encoded) == 0 {
		t.Fatal("baseline descriptor missing")
	}
	old := &descriptorpb.FileDescriptorProto{}
	if err := proto.Unmarshal(encoded, old); err != nil {
		t.Fatal(err)
	}
	current := protodesc.ToFileDescriptorProto(pb.File_inventory_proto)
	if old.GetPackage() != current.GetPackage() || old.GetOptions().GetGoPackage() != current.GetOptions().GetGoPackage() {
		t.Fatal("package changed")
	}
	compareMessages(t, old.GetMessageType(), current.GetMessageType())
	for _, service := range old.GetService() {
		var found *descriptorpb.ServiceDescriptorProto
		for _, candidate := range current.GetService() {
			if candidate.GetName() == service.GetName() {
				found = candidate
			}
		}
		if found == nil {
			t.Fatalf("service %s removed", service.GetName())
		}
		for _, method := range service.GetMethod() {
			var match *descriptorpb.MethodDescriptorProto
			for _, candidate := range found.GetMethod() {
				if candidate.GetName() == method.GetName() {
					match = candidate
				}
			}
			if !proto.Equal(method, match) {
				t.Errorf("RPC %s changed or removed", method.GetName())
			}
		}
	}
}

func compareMessages(t *testing.T, old, current []*descriptorpb.DescriptorProto) {
	t.Helper()
	for _, message := range old {
		var found *descriptorpb.DescriptorProto
		for _, candidate := range current {
			if candidate.GetName() == message.GetName() {
				found = candidate
			}
		}
		if found == nil {
			t.Fatalf("message %s removed", message.GetName())
		}
		for _, field := range message.GetField() {
			var match *descriptorpb.FieldDescriptorProto
			for _, candidate := range found.GetField() {
				if candidate.GetNumber() == field.GetNumber() {
					match = candidate
				}
			}
			if !proto.Equal(field, match) {
				t.Errorf("%s.%s changed or removed", message.GetName(), field.GetName())
			}
		}
		compareMessages(t, message.GetNestedType(), found.GetNestedType())
		for _, enum := range message.GetEnumType() {
			var match *descriptorpb.EnumDescriptorProto
			for _, candidate := range found.GetEnumType() {
				if candidate.GetName() == enum.GetName() {
					match = candidate
				}
			}
			if !proto.Equal(enum, match) {
				t.Errorf("enum %s.%s changed", message.GetName(), enum.GetName())
			}
		}
	}
}
