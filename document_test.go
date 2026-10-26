package luxir

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/aria-lopez/go-luxir/luxirpb"
)

// TestToVal ;; every supported Go type becomes the expected protobuf *Val
//
// ;; protobuf messages must be compared with proto.Equal, not == or reflect.DeepEqual
func TestToVal(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want *luxirpb.Val
	}{
		{"nil", nil, &luxirpb.Val{Kind: &luxirpb.Val_Null{Null: structpb.NullValue_NULL_VALUE}}},

		// ;; scalars
		{"string", "dune", &luxirpb.Val{Kind: &luxirpb.Val_S{S: "dune"}}},
		{"empty string", "", &luxirpb.Val{Kind: &luxirpb.Val_S{S: ""}}},
		{"bool true", true, &luxirpb.Val{Kind: &luxirpb.Val_B{B: true}}},
		{"bool false", false, &luxirpb.Val{Kind: &luxirpb.Val_B{B: false}}},
		{"bytes", []byte("raw"), &luxirpb.Val{Kind: &luxirpb.Val_Bin{Bin: []byte("raw")}}},

		// ;; every integer width ends up as the single int64 kind
		{"int", int(1965), &luxirpb.Val{Kind: &luxirpb.Val_I{I: 1965}}},
		{"int negative", int(-7), &luxirpb.Val{Kind: &luxirpb.Val_I{I: -7}}},
		{"int8", int8(-8), &luxirpb.Val{Kind: &luxirpb.Val_I{I: -8}}},
		{"int16", int16(-16), &luxirpb.Val{Kind: &luxirpb.Val_I{I: -16}}},
		{"int32", int32(-32), &luxirpb.Val{Kind: &luxirpb.Val_I{I: -32}}},
		{"int64", int64(1 << 62), &luxirpb.Val{Kind: &luxirpb.Val_I{I: 1 << 62}}},
		{"uint8", uint8(255), &luxirpb.Val{Kind: &luxirpb.Val_I{I: 255}}},
		{"uint16", uint16(65535), &luxirpb.Val{Kind: &luxirpb.Val_I{I: 65535}}},
		{"uint32 max", uint32(4294967295), &luxirpb.Val{Kind: &luxirpb.Val_I{I: 4294967295}}},

		// ;; times are epoch millis, whatever the zone
		{"time epoch", time.UnixMilli(0), &luxirpb.Val{Kind: &luxirpb.Val_I{I: 0}}},
		{"time before 1970", time.Date(1965, 8, 1, 0, 0, 0, 0, time.UTC), &luxirpb.Val{Kind: &luxirpb.Val_I{I: -139449600000}}},
		{"time other zone", time.Date(1965, 8, 1, 2, 0, 0, 0, time.FixedZone("x", 2*3600)), &luxirpb.Val{Kind: &luxirpb.Val_I{I: -139449600000}}},

		// ;; floats keep their width
		{"float32", float32(1.5), &luxirpb.Val{Kind: &luxirpb.Val_F{F: 1.5}}},
		{"float64", float64(2.5), &luxirpb.Val{Kind: &luxirpb.Val_D{D: 2.5}}},

		// ;; typed slices become typed arrays
		{"[]string", []string{"a", "b"}, &luxirpb.Val{Kind: &luxirpb.Val_ArrS{ArrS: &luxirpb.ArrStr{V: []string{"a", "b"}}}}},
		{"[]string empty", []string{}, &luxirpb.Val{Kind: &luxirpb.Val_ArrS{ArrS: &luxirpb.ArrStr{}}}},
		{"[]int64", []int64{1, 2}, &luxirpb.Val{Kind: &luxirpb.Val_ArrI{ArrI: &luxirpb.ArrInt{V: []int64{1, 2}}}}},
		{"[]int widened", []int{3, 4}, &luxirpb.Val{Kind: &luxirpb.Val_ArrI{ArrI: &luxirpb.ArrInt{V: []int64{3, 4}}}}},
		{"[]float32", []float32{1.5, 2.5}, &luxirpb.Val{Kind: &luxirpb.Val_ArrF{ArrF: &luxirpb.ArrFloat{V: []float32{1.5, 2.5}}}}},
		{"[]float64", []float64{1.5, 2.5}, &luxirpb.Val{Kind: &luxirpb.Val_ArrD{ArrD: &luxirpb.ArrDouble{V: []float64{1.5, 2.5}}}}},
		{"[][]byte", [][]byte{[]byte("a"), []byte("b")}, &luxirpb.Val{Kind: &luxirpb.Val_ArrBin{ArrBin: &luxirpb.ArrBin{V: [][]byte{[]byte("a"), []byte("b")}}}}},

		{"point is lon lat", Point{Lat: 40.7, Lon: -74}, &luxirpb.Val{Kind: &luxirpb.Val_ArrD{ArrD: &luxirpb.ArrDouble{V: []float64{-74, 40.7}}}}},
		{"[]Point", []Point{{Lat: 1, Lon: 2}, {Lat: 3, Lon: 4}}, &luxirpb.Val{Kind: &luxirpb.Val_Arr{Arr: &luxirpb.ArrVal{V: []*luxirpb.Val{
			{Kind: &luxirpb.Val_ArrD{ArrD: &luxirpb.ArrDouble{V: []float64{2, 1}}}},
			{Kind: &luxirpb.Val_ArrD{ArrD: &luxirpb.ArrDouble{V: []float64{4, 3}}}},
		}}}}},
		{"[][]float64", [][]float64{{-74, 40.7}, {-0.1, 51.5}}, &luxirpb.Val{Kind: &luxirpb.Val_Arr{Arr: &luxirpb.ArrVal{V: []*luxirpb.Val{
			{Kind: &luxirpb.Val_ArrD{ArrD: &luxirpb.ArrDouble{V: []float64{-74, 40.7}}}},
			{Kind: &luxirpb.Val_ArrD{ArrD: &luxirpb.ArrDouble{V: []float64{-0.1, 51.5}}}},
		}}}}},
		{"[][]float32 multi vector", [][]float32{{1, 2}, {3, 4}}, &luxirpb.Val{Kind: &luxirpb.Val_ArrVec{ArrVec: &luxirpb.ArrVector{V: []*luxirpb.Vector{
			{Kind: &luxirpb.Vector_F32{F32: &luxirpb.ArrFloat{V: []float32{1, 2}}}},
			{Kind: &luxirpb.Vector_F32{F32: &luxirpb.ArrFloat{V: []float32{3, 4}}}},
		}}}}},

		// ;; []any converts element by element, and may mix kinds
		{"[]any mixed", []any{"a", 1, 2.5}, &luxirpb.Val{Kind: &luxirpb.Val_Arr{Arr: &luxirpb.ArrVal{V: []*luxirpb.Val{
			{Kind: &luxirpb.Val_S{S: "a"}},
			{Kind: &luxirpb.Val_I{I: 1}},
			{Kind: &luxirpb.Val_D{D: 2.5}},
		}}}}},
		{"[]any nested slice", []any{[]string{"x"}}, &luxirpb.Val{Kind: &luxirpb.Val_Arr{Arr: &luxirpb.ArrVal{V: []*luxirpb.Val{
			{Kind: &luxirpb.Val_ArrS{ArrS: &luxirpb.ArrStr{V: []string{"x"}}}},
		}}}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toVal(tt.in)
			if err != nil {
				t.Fatalf("toVal(%#v): %v", tt.in, err)
			}
			if !proto.Equal(got, tt.want) {
				t.Errorf("toVal(%#v)\n got:  %v\n want: %v", tt.in, got, tt.want)
			}
		})
	}
}

// TestToVal_Unsupported ;; types we deliberately do not convert must return an error
func TestToVal_Unsupported(t *testing.T) {
	type custom struct{}

	tests := []struct {
		name string
		in   any
	}{
		{"uint (can overflow int64)", uint(1)},
		{"uint64 (can overflow int64)", uint64(1)},
		{"[]bool (no array kind)", []bool{true}},
		{"nested map", map[string]any{"k": "v"}},
		{"nested Document", Document{"k": "v"}},
		{"struct", custom{}},
		{"pointer", new(int)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toVal(tt.in)
			if err == nil {
				t.Fatalf("toVal(%T) = %v, want an error", tt.in, got)
			}
			if got != nil {
				t.Errorf("toVal(%T) returned a value alongside the error: %v", tt.in, got)
			}
		})
	}
}

// TestToVal_BadElement ;; a bad element inside []any is reported with its position
func TestToVal_BadElement(t *testing.T) {
	_, err := toVal([]any{"ok", struct{}{}, "ok"})
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "element 1") {
		t.Errorf("err = %q, want it to mention \"element 1\"", err)
	}
}

// TestToMap ;; a Document becomes a *Map with one converted Val per field
func TestToMap(t *testing.T) {
	doc := Document{
		"id":      "b1",
		"title_t": "Dune",
		"year_i":  1965,
		"tags_ss": []string{"scifi", "classic"},
		"note_s":  nil,
	}

	want := &luxirpb.Map{Fields: map[string]*luxirpb.Val{
		"id":      {Kind: &luxirpb.Val_S{S: "b1"}},
		"title_t": {Kind: &luxirpb.Val_S{S: "Dune"}},
		"year_i":  {Kind: &luxirpb.Val_I{I: 1965}},
		"tags_ss": {Kind: &luxirpb.Val_ArrS{ArrS: &luxirpb.ArrStr{V: []string{"scifi", "classic"}}}},
		"note_s":  {Kind: &luxirpb.Val_Null{Null: structpb.NullValue_NULL_VALUE}},
	}}

	got, err := doc.toMap()
	if err != nil {
		t.Fatalf("toMap: %v", err)
	}
	if !proto.Equal(got, want) {
		t.Errorf("toMap\n got:  %v\n want: %v", got, want)
	}
}

// TestToMap_Empty ;; an empty or nil Document is valid and gives an empty *Map
func TestToMap_Empty(t *testing.T) {
	for name, doc := range map[string]Document{"empty": {}, "nil": nil} {
		t.Run(name, func(t *testing.T) {
			got, err := doc.toMap()
			if err != nil {
				t.Fatalf("toMap: %v", err)
			}
			if got == nil {
				t.Fatal("toMap returned a nil *Map")
			}
			if n := len(got.GetFields()); n != 0 {
				t.Errorf("got %d fields, want 0", n)
			}
		})
	}
}

// TestToMap_BadField ;; the error names the offending field, and no partial Map leaks out
func TestToMap_BadField(t *testing.T) {
	doc := Document{"id": "x", "broken_s": struct{}{}}

	got, err := doc.toMap()
	if err == nil {
		t.Fatalf("toMap = %v, want an error", got)
	}
	if got != nil {
		t.Errorf("toMap returned a *Map alongside the error: %v", got)
	}
	if !strings.Contains(err.Error(), `"broken_s"`) {
		t.Errorf("err = %q, want it to name the field \"broken_s\"", err)
	}
}

// TestToMap_BadNestedElement ;; field name and element position both show up
func TestToMap_BadNestedElement(t *testing.T) {
	_, err := Document{"list_ss": []any{"ok", struct{}{}}}.toMap()
	if err == nil {
		t.Fatal("want an error")
	}
	for _, part := range []string{`"list_ss"`, "element 1"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("err = %q, want it to contain %s", err, part)
		}
	}
}
