package bencode

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBencode_Encode(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		input   any
		wantErr error
		wantVal any
	}

	cases := []testCase{
		{
			name:    "byte string: string",
			input:   "alice",
			wantVal: "5:alice",
		},
		{
			name:    "byte string: empty string",
			input:   "",
			wantVal: "0:",
		},
		// {
		// 	name:    "byte slice: binary data",
		// 	input:   []byte{0x00, 0x01, 0x02},
		// 	wantVal: string([]byte{3, ':', 0x00, 0x01, 0x02})[1:],
		// },
		{
			name:    "int: basic int32",
			input:   int32(2),
			wantVal: "i2e",
		},
		{
			name:    "int: negative int64",
			input:   int64(-10),
			wantVal: "i-10e",
		},
		{
			name:    "int: uint8",
			input:   uint8(8),
			wantVal: "i8e",
		},
		{
			name:    "int: uint",
			input:   uint(32),
			wantVal: "i32e",
		},
		{
			name:    "int: uint64",
			input:   uint64(32),
			wantVal: "i32e",
		},
		{
			name:    "int: basic",
			input:   2,
			wantVal: "i2e",
		},
		{
			name:    "int: zero",
			input:   0,
			wantVal: "i0e",
		},
		{
			name:    "int: negative integer",
			input:   -10,
			wantVal: "i-10e",
		},
		{
			name:    "int: large",
			input:   int64(1043002),
			wantVal: "i1043002e",
		},
		{
			name:    "list: strings only",
			input:   []string{"spam", "age"},
			wantVal: "l4:spam3:agee",
		},
		{
			name:    "list: strings with ints (string ints remain strings)",
			input:   []string{"micheal", "jordan", "23"},
			wantVal: "l7:micheal6:jordan2:23e",
		},
		{
			name:    "list: ints",
			input:   []int{1, 2, 3, 5, 8, 12},
			wantVal: "li1ei2ei3ei5ei8ei12ee",
		},
		{
			name:    "list: mixed types",
			input:   []any{"a", 1, []string{"b"}},
			wantVal: "l1:ai1el1:bee",
		},
		{
			name:    "list: nested empty list",
			input:   []any{[]any{}},
			wantVal: "llee",
			wantErr: nil,
		},
		{
			name:    "list: dictionaries single-key entries",
			input:   []any{map[string]any{"a": 1}, map[string]any{"b": "bee"}},
			wantVal: "ld1:ai1eed1:b3:beeee",
		},
		{
			name:    "list: dictionary with nested list value",
			input:   []any{map[string]any{"k": []int{1, 2}}},
			wantVal: "ld1:kli1ei2eeee",
		},
		{
			name:    "list: dictionary with multiple keys",
			input:   []any{map[string]any{"bar": "spam", "foo": 42}},
			wantVal: "ld3:bar4:spam3:fooi42eee",
		},
		{
			name:    "list: dictionary with non-string key (error)",
			input:   []any{map[any]any{1: "a"}},
			wantErr: ErrDictKeyNotString,
		},
		{
			name:    "dictionary: empty",
			input:   map[string]any{},
			wantVal: "de",
		},
		{
			name: "dictionary: simple",
			input: map[string]any{
				"bar":  "spam",
				"foo":  "eggs",
				"zero": 0,
			},
			wantVal: "d3:bar4:spam3:foo4:eggs4:zeroi0ee",
		},
		{
			name: "dictionary: simple sorted keys",
			input: map[string]any{
				"bar":   "spam",
				"foo":   "eggs",
				"zero":  0,
				"alias": "myalias",
			},
			wantVal: "d5:alias7:myalias3:bar4:spam3:foo4:eggs4:zeroi0ee",
		},
		{
			name:    "dictionary: nested info",
			input:   map[string]any{"info": map[string]any{"length": 1, "name": "a"}},
			wantVal: "d4:infod6:lengthi1e4:name1:aee",
		},
		{
			name:    "dictionary: non-string key",
			input:   map[any]any{1: "a"},
			wantErr: ErrDictKeyNotString,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value, err := Marshal(tc.input)
			if tc.wantErr != nil {
				require.Error(t, err)
				require.Equal(t, err, tc.wantErr)
				require.Nil(t, value)
			} else {
				require.NoError(t, err)
				require.Equal(t, string(value), tc.wantVal)
			}
		})
	}
}
