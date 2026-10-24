package luxir

import (
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

// Document ;; one record to index, [field name]->[value]
// ;; the field names suffix tells Luxir the type (like title_t, year_i, etc)
type Document map[string]any

// toMap ;; change a document to a protobuf *Map
func (d Document) toMap() (*luxirpb.Map, error) {
	fields := make(map[string]*luxirpb.Val, len(d))
	for name, v := range d {
		val, err := toVal(v)
		if err != nil {
			return nil, fmt.Errorf("field: %q: %w", name, err)
		}
		fields[name] = val
	}
	return &luxirpb.Map{Fields: fields}, nil
}

// toVal ;; Change goes native value to a protobuf *Val
func toVal(v any) (*luxirpb.Val, error) {
	switch x := v.(type) {
	case nil:
		return &luxirpb.Val{Kind: &luxirpb.Val_Null{Null: structpb.NullValue_NULL_VALUE}}, nil
	case string:
		return &luxirpb.Val{Kind: &luxirpb.Val_S{S: x}}, nil
	case bool:
		return &luxirpb.Val{Kind: &luxirpb.Val_B{B: x}}, nil
	case []byte:
		return &luxirpb.Val{Kind: &luxirpb.Val_Bin{Bin: x}}, nil
	case int:
		return &luxirpb.Val{Kind: &luxirpb.Val_I{I: int64(x)}}, nil
	case int8:
		return &luxirpb.Val{Kind: &luxirpb.Val_I{I: int64(x)}}, nil
	case int16:
		return &luxirpb.Val{Kind: &luxirpb.Val_I{I: int64(x)}}, nil
	case int32:
		return &luxirpb.Val{Kind: &luxirpb.Val_I{I: int64(x)}}, nil
	case int64:
		return &luxirpb.Val{Kind: &luxirpb.Val_I{I: x}}, nil
	case uint8:
		return &luxirpb.Val{Kind: &luxirpb.Val_I{I: int64(x)}}, nil
	case uint16:
		return &luxirpb.Val{Kind: &luxirpb.Val_I{I: int64(x)}}, nil
	case uint32:
		return &luxirpb.Val{Kind: &luxirpb.Val_I{I: int64(x)}}, nil
	case float32:
		return &luxirpb.Val{Kind: &luxirpb.Val_F{F: x}}, nil
	case float64:
		return &luxirpb.Val{Kind: &luxirpb.Val_D{D: x}}, nil
	case []string:
		return &luxirpb.Val{Kind: &luxirpb.Val_ArrS{ArrS: &luxirpb.ArrStr{V: x}}}, nil
	case []int64:
		return &luxirpb.Val{Kind: &luxirpb.Val_ArrI{ArrI: &luxirpb.ArrInt{V: x}}}, nil
	case []int:
		ints := make([]int64, len(x))
		for i, n := range x {
			ints[i] = int64(n)
		}
		return &luxirpb.Val{Kind: &luxirpb.Val_ArrI{ArrI: &luxirpb.ArrInt{V: ints}}}, nil
	case []float32:
		return &luxirpb.Val{Kind: &luxirpb.Val_ArrF{ArrF: &luxirpb.ArrFloat{V: x}}}, nil
	case []float64:
		return &luxirpb.Val{Kind: &luxirpb.Val_ArrD{ArrD: &luxirpb.ArrDouble{V: x}}}, nil
	case [][]byte:
		return &luxirpb.Val{Kind: &luxirpb.Val_ArrBin{ArrBin: &luxirpb.ArrBin{V: x}}}, nil
	case []any:
		vals := make([]*luxirpb.Val, len(x))
		for i, e := range x {
			val, err := toVal(e)
			if err != nil {
				return nil, fmt.Errorf("element %d: %w", i, err)
			}
			vals[i] = val
		}
		return &luxirpb.Val{Kind: &luxirpb.Val_Arr{Arr: &luxirpb.ArrVal{V: vals}}}, nil
	default:
		return nil, fmt.Errorf("insupported type %T", v)
	}
}
