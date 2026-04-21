package tools_test

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agent-harness/go/tools"
)

// ---- simple struct --------------------------------------------------------

type SimpleArgs struct {
	Name string `json:"name" desc:"the name"`
	Age  int    `json:"age"`
}

func TestSchema_SimpleStruct(t *testing.T) {
	s := tools.Schema(reflect.TypeOf(SimpleArgs{}))

	assert.Equal(t, "object", s["type"])

	props, ok := s["properties"].(map[string]any)
	require.True(t, ok, "properties should be map[string]any")

	nameProp, ok := props["name"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "string", nameProp["type"])
	assert.Equal(t, "the name", nameProp["description"])

	ageProp, ok := props["age"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "integer", ageProp["type"])

	required, ok := s["required"].([]string)
	require.True(t, ok)
	assert.ElementsMatch(t, []string{"name", "age"}, required)
}

// ---- omitempty means optional ---------------------------------------------

type OptionalArgs struct {
	Required string `json:"required"`
	Optional string `json:"optional,omitempty"`
}

func TestSchema_Omitempty(t *testing.T) {
	s := tools.Schema(reflect.TypeOf(OptionalArgs{}))

	required, ok := s["required"].([]string)
	require.True(t, ok)
	assert.Equal(t, []string{"required"}, required)
}

// ---- json rename ----------------------------------------------------------

type RenameArgs struct {
	MyField string `json:"my_field"`
}

func TestSchema_JSONRename(t *testing.T) {
	s := tools.Schema(reflect.TypeOf(RenameArgs{}))
	props := s["properties"].(map[string]any)
	_, ok := props["my_field"]
	assert.True(t, ok, "should use json tag name not Go name")
	_, okOld := props["MyField"]
	assert.False(t, okOld, "Go name should not appear")
}

// ---- nested struct --------------------------------------------------------

type AddressArgs struct {
	Street string `json:"street"`
}

type PersonArgs struct {
	Name    string      `json:"name"`
	Address AddressArgs `json:"address"`
}

func TestSchema_NestedStruct(t *testing.T) {
	s := tools.Schema(reflect.TypeOf(PersonArgs{}))
	props := s["properties"].(map[string]any)

	addrProp, ok := props["address"].(map[string]any)
	require.True(t, ok, "address should be a nested object")
	assert.Equal(t, "object", addrProp["type"])

	nestedProps, ok := addrProp["properties"].(map[string]any)
	require.True(t, ok)
	streetProp, ok := nestedProps["street"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "string", streetProp["type"])
}

// ---- slice of string ------------------------------------------------------

type TagsArgs struct {
	Tags []string `json:"tags"`
}

func TestSchema_SliceOfString(t *testing.T) {
	s := tools.Schema(reflect.TypeOf(TagsArgs{}))
	props := s["properties"].(map[string]any)
	tagsProp := props["tags"].(map[string]any)
	assert.Equal(t, "array", tagsProp["type"])
	items := tagsProp["items"].(map[string]any)
	assert.Equal(t, "string", items["type"])
}

// ---- slice of struct -------------------------------------------------------

type ItemArgs struct {
	ID int `json:"id"`
}

type OrderArgs struct {
	Items []ItemArgs `json:"items"`
}

func TestSchema_SliceOfStruct(t *testing.T) {
	s := tools.Schema(reflect.TypeOf(OrderArgs{}))
	props := s["properties"].(map[string]any)
	itemsProp := props["items"].(map[string]any)
	assert.Equal(t, "array", itemsProp["type"])
	items := itemsProp["items"].(map[string]any)
	assert.Equal(t, "object", items["type"])
}

// ---- map[string]any -------------------------------------------------------

type BlobArgs struct {
	Meta map[string]any `json:"meta"`
}

func TestSchema_MapStringAny(t *testing.T) {
	s := tools.Schema(reflect.TypeOf(BlobArgs{}))
	props := s["properties"].(map[string]any)
	metaProp := props["meta"].(map[string]any)
	assert.Equal(t, "object", metaProp["type"])
}

// ---- numeric types --------------------------------------------------------

type NumericArgs struct {
	IntVal   int     `json:"int_val"`
	Int32Val int32   `json:"int32_val"`
	Int64Val int64   `json:"int64_val"`
	F32Val   float32 `json:"f32_val"`
	F64Val   float64 `json:"f64_val"`
}

func TestSchema_NumericTypes(t *testing.T) {
	s := tools.Schema(reflect.TypeOf(NumericArgs{}))
	props := s["properties"].(map[string]any)

	assert.Equal(t, "integer", props["int_val"].(map[string]any)["type"])
	assert.Equal(t, "integer", props["int32_val"].(map[string]any)["type"])
	assert.Equal(t, "integer", props["int64_val"].(map[string]any)["type"])
	assert.Equal(t, "number", props["f32_val"].(map[string]any)["type"])
	assert.Equal(t, "number", props["f64_val"].(map[string]any)["type"])
}

// ---- bool -----------------------------------------------------------------

type BoolArgs struct {
	Flag bool `json:"flag"`
}

func TestSchema_Bool(t *testing.T) {
	s := tools.Schema(reflect.TypeOf(BoolArgs{}))
	props := s["properties"].(map[string]any)
	assert.Equal(t, "boolean", props["flag"].(map[string]any)["type"])
}

// ---- unsupported kinds panic ----------------------------------------------

type BadArgsFunc struct {
	F func() `json:"f"`
}

type BadArgsChan struct {
	C chan int `json:"c"`
}

type BadArgsIface struct {
	I interface{ Foo() } `json:"i"`
}

func TestSchema_PanicsOnFunc(t *testing.T) {
	assert.Panics(t, func() {
		tools.Schema(reflect.TypeOf(BadArgsFunc{}))
	})
}

func TestSchema_PanicsOnChan(t *testing.T) {
	assert.Panics(t, func() {
		tools.Schema(reflect.TypeOf(BadArgsChan{}))
	})
}

func TestSchema_PanicsOnInterface(t *testing.T) {
	assert.Panics(t, func() {
		tools.Schema(reflect.TypeOf(BadArgsIface{}))
	})
}
