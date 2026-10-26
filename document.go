package luxir

import (
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

// Document ;; one record to index, [field name]->[value]
// ;; the field names suffix tells Luxir the type (like title_t, year_i, etc)
// ;; values: string, bool, ints, floats, time.Time, Point, their slices, []float32 vectors, [][]float32 multi-vectors
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
	case time.Time:
		return &luxirpb.Val{Kind: &luxirpb.Val_I{I: x.UnixMilli()}}, nil
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
	case Point:
		return arrD(x.Lon, x.Lat), nil
	case []Point:
		vals := make([]*luxirpb.Val, len(x))
		for i, p := range x {
			vals[i] = arrD(p.Lon, p.Lat)
		}
		return &luxirpb.Val{Kind: &luxirpb.Val_Arr{Arr: &luxirpb.ArrVal{V: vals}}}, nil
	case [][]float64:
		vals := make([]*luxirpb.Val, len(x))
		for i, v := range x {
			vals[i] = arrD(v...)
		}
		return &luxirpb.Val{Kind: &luxirpb.Val_Arr{Arr: &luxirpb.ArrVal{V: vals}}}, nil
	case [][]float32:
		vecs := make([]*luxirpb.Vector, len(x))
		for i, v := range x {
			vecs[i] = &luxirpb.Vector{Kind: &luxirpb.Vector_F32{F32: &luxirpb.ArrFloat{V: v}}}
		}
		return &luxirpb.Val{Kind: &luxirpb.Val_ArrVec{ArrVec: &luxirpb.ArrVector{V: vecs}}}, nil
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
		return nil, fmt.Errorf("unsupported type %T", v)
	}
}

// arrD ;; a double array Val, geo points go [lon, lat]
func arrD(v ...float64) *luxirpb.Val {
	return &luxirpb.Val{Kind: &luxirpb.Val_ArrD{ArrD: &luxirpb.ArrDouble{V: v}}}
}

// fromVal ;; Change a protobuf *Val to a native value, the inverse of toVal
//
// ;; ints come back as int64 and int arrays as []int64, whatever went in
func fromVal(v *luxirpb.Val) (any, error) {
	switch x := v.GetKind().(type) {
	case nil, *luxirpb.Val_Null:
		return nil, nil
	case *luxirpb.Val_S:
		return x.S, nil
	case *luxirpb.Val_B:
		return x.B, nil
	case *luxirpb.Val_Bin:
		return x.Bin, nil
	case *luxirpb.Val_I:
		return x.I, nil
	case *luxirpb.Val_F:
		return x.F, nil
	case *luxirpb.Val_D:
		return x.D, nil
	case *luxirpb.Val_ArrS:
		return x.ArrS.GetV(), nil
	case *luxirpb.Val_ArrI:
		return x.ArrI.GetV(), nil
	case *luxirpb.Val_ArrF:
		return x.ArrF.GetV(), nil
	case *luxirpb.Val_ArrD:
		return x.ArrD.GetV(), nil
	case *luxirpb.Val_ArrBin:
		return x.ArrBin.GetV(), nil
	case *luxirpb.Val_Vec:
		return x.Vec.GetF32().GetV(), nil
	case *luxirpb.Val_ArrVec:
		out := make([][]float32, len(x.ArrVec.GetV()))
		for i, v := range x.ArrVec.GetV() {
			out[i] = v.GetF32().GetV()
		}
		return out, nil
	case *luxirpb.Val_Arr:
		out := make([]any, len(x.Arr.GetV()))
		for i, e := range x.Arr.GetV() {
			val, err := fromVal(e)
			if err != nil {
				return nil, fmt.Errorf("element %d: %w", i, err)
			}
			out[i] = val
		}
		return out, nil
	case *luxirpb.Val_Map:
		return fromMap(x.Map)
	default:
		return nil, fmt.Errorf("unsupported value %T", x)
	}
}

// fromMap ;; Change a protobuf *Map to a Document, the inverse of Document.toMap
func fromMap(m *luxirpb.Map) (Document, error) {
	d := make(Document, len(m.GetFields()))
	for name, v := range m.GetFields() {
		val, err := fromVal(v)
		if err != nil {
			return nil, fmt.Errorf("field: %q: %w", name, err)
		}
		d[name] = val
	}
	return d, nil
}
