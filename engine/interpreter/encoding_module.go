package interpreter

import (
	"encoding/base64"

	"github.com/codong-lang/codong/stdlib/codongerror"
)

// EncodingModuleObject represents the encoding module.
type EncodingModuleObject struct{}

func (e *EncodingModuleObject) Type() string    { return "module" }
func (e *EncodingModuleObject) Inspect() string { return "encoding" }

var encodingModuleSingleton = &EncodingModuleObject{}

// evalEncodingModuleMethod evaluates encoding module method calls.
func (i *Interpreter) evalEncodingModuleMethod(method string) Object {
	switch method {
	case "base64_decode":
		return &BuiltinFunction{
			Name: "base64_decode",
			Fn: func(interp *Interpreter, args ...Object) Object {
				if len(args) < 1 {
					return &StringObject{Value: ""}
				}
				str, ok := args[0].(*StringObject)
				if !ok {
					return &StringObject{Value: ""}
				}
				decoded, err := base64.StdEncoding.DecodeString(str.Value)
				if err != nil {
					return &StringObject{Value: ""}
				}
				return &StringObject{Value: string(decoded)}
			},
		}

	case "base64_encode":
		return &BuiltinFunction{
			Name: "base64_encode",
			Fn: func(interp *Interpreter, args ...Object) Object {
				if len(args) < 1 {
					return &StringObject{Value: ""}
				}
				str, ok := args[0].(*StringObject)
				if !ok {
					return &StringObject{Value: ""}
				}
				encoded := base64.StdEncoding.EncodeToString([]byte(str.Value))
				return &StringObject{Value: encoded}
			},
		}
	}

	return newRuntimeError(codongerror.E1003_UNDEFINED_VAR,
		"encoding."+method+" is not defined",
		"available encoding methods: base64_decode, base64_encode")
}
