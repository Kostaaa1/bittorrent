package bencode

import (
	"bytes"
	"io"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

type testCase[T any] struct {
	name  string
	input string
	err   error
	want  T
}

func run[T any](t *testing.T, cases []testCase[T]) {
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			t.Logf("Testing for input: %s\n", tc.input)

			buf := bytes.NewBuffer([]byte(tc.input))

			// var data interface{}
			var data T
			err := NewDecoder(buf).Decode(&data)

			if tc.err != nil {
				require.Error(t, err)
				require.Equal(t, err, tc.err)
				require.Zero(t, data)
			} else {
				require.NoError(t, err)
				require.True(t, reflect.DeepEqual(data, tc.want))
			}
		})
	}
}

func TestDecode_Int(t *testing.T) {
	run(t, []testCase[int]{
		{
			name:  "int",
			input: "i32e",
			want:  32,
		},
		{
			name:  "int",
			input: "i-32e",
			want:  -32,
		},
		{
			name:  "int",
			input: "i0e",
			want:  0,
		},
		{
			name:  "int",
			input: "i20043e",
			want:  20043,
		},
		{
			name:  "int",
			input: "i1043002e",
			want:  1043002,
		},
		{
			name:  "int - invalid",
			input: "i-0e",
			err:   ErrInvalidIntegerFormat,
		},
		{
			name:  "int - invalid",
			input: "i-23-4-2e",
			err:   ErrInvalidIntegerFormat,
		},
		{
			name:  "int - invalid",
			input: "i1-3e",
			err:   ErrInvalidIntegerFormat,
		},
		{
			name:  "int - invalid",
			input: "i03e",
			err:   ErrInvalidIntegerFormat,
		},
		{
			name:  "int: missing terminator",
			input: "i32",
			err:   ErrInvalidIntegerFormat,
		},
		{
			name:  "int: missing integer",
			input: "i-e",
			err:   ErrInvalidIntegerFormat,
		},
		{
			name:  "int - plus sign invalid",
			input: "i+32e",
			err:   ErrInvalidIntegerFormat,
		},
		{
			name:  "int - non-digit characters",
			input: "i12a3e",
			err:   ErrInvalidIntegerFormat,
		},
		{
			name:  "int - space inside number",
			input: "i1 3e",
			err:   ErrInvalidIntegerFormat,
		},
	})
}

func TestDecode_String(t *testing.T) {
	run(t, []testCase[string]{
		{
			name:  "string: invalid format",
			input: "",
			want:  "",
			err:   io.EOF,
		},
		{
			name:  "string: detected as int (first byte)",
			input: "ilovesemantics",
			want:  "",
			err:   ErrInvalidIntegerFormat,
		},
		{
			name:  "string: missing colon",
			input: "5alice",
			want:  "",
			err:   ErrInvalidStringFormat,
		},
		{
			name:  "string: invalid - length < string length",
			input: "4:alicealice",
			want:  "",
			err:   ErrTrailingDataLeft,
		},
		{
			name:  "string: invalid - length > string length",
			input: "5:eggs",
			want:  "",
			err:   io.ErrUnexpectedEOF,
		},
		{
			name:  "string: invalid - negative number",
			input: "-5:eggs",
			want:  "",
			err:   ErrInvalidStringFormat,
		},
		{
			name:  "string: leading zero length",
			input: "03:abc",
			want:  "",
			err:   ErrInvalidStringFormat,
		},
		{
			name:  "string: leading zero zero",
			input: "00:",
			want:  "",
			err:   ErrInvalidStringFormat,
		},
		{
			name:  "string: short",
			input: "5:Alice",
			want:  "Alice",
		},
		{
			name:  "string: long",
			input: "20:alicealicealicealice",
			want:  "alicealicealicealice",
		},
		{
			name:  "string: empty",
			input: "0:",
			want:  "",
		},
	})
}

func TestDecode_List(t *testing.T) {
	run(t, []testCase[[]interface{}]{
		{
			name:  "list: strings and ints",
			input: "l5:hello5:worldi123e3:abce",
			want:  []interface{}{"hello", "world", 123, "abc"},
		},
		{
			name:  "list: strings and ints",
			input: "l5:helloi52ee",
			want:  []interface{}{"hello", 52},
		},
		{
			name:  "list: ints",
			input: "li32ei25ee",
			want:  []interface{}{32, 25},
		},
	})
}

func TestDecode_Dictionary(t *testing.T) {
	run(t, []testCase[map[string]interface{}]{
		{
			name:  "dictionary: empty",
			input: "de",
			want:  map[string]interface{}{},
		},
		{
			name:  "dictionary",
			input: "d4:infod4:name5:b.txt6:lengthi1eee",
			want: map[string]interface{}{
				"info": map[string]interface{}{"name": "b.txt", "length": 1},
			},
		},
		{
			name:  "dictionary",
			input: "d6:client11:ArchTorrent7:versioni5ee",
			want: map[string]interface{}{
				"client":  "ArchTorrent",
				"version": 5,
			},
		},
		{
			name:  "dictionary",
			input: "di32e7:versioni5ee",
			want:  nil,
			err:   ErrDictKeyNotString,
		},
		{
			name:  "dictionary: torrent example",
			input: "d8:announce23:http://bt4.t-ru.org/ann13:announce-listll23:http://bt4.t-ru.org/annel31:http://retracker.local/announceee7:comment51:https://rutracker.org/forum/viewtopic.php?t=649613210:created by13:BitComet/2.0513:creation datei1709731450e8:encoding5:UTF-84:infod6:lengthi20028000e4:name52:Atkins Evan - GoLang for Machine Learning - 2024.PDF10:name.utf-852:Atkins Evan - GoLang for Machine Learning - 2024.PDF12:piece lengthi65536ee9:publisher13:rutracker.org13:publisher-url51:https://rutracker.org/forum/viewtopic.php?t=6496132e",
			want: map[string]interface{}{
				"announce":      "http://bt4.t-ru.org/ann",
				"announce-list": []interface{}{[]interface{}{"http://bt4.t-ru.org/ann"}, []interface{}{"http://retracker.local/announce"}},
				"comment":       "https://rutracker.org/forum/viewtopic.php?t=6496132",
				"created by":    "BitComet/2.05",
				"creation date": 1709731450,
				"encoding":      "UTF-8",
				"info": map[string]interface{}{
					"length":       20028000,
					"name":         "Atkins Evan - GoLang for Machine Learning - 2024.PDF",
					"name.utf-8":   "Atkins Evan - GoLang for Machine Learning - 2024.PDF",
					"piece length": 65536,
				},
				"publisher":     "rutracker.org",
				"publisher-url": "https://rutracker.org/forum/viewtopic.php?t=6496132",
			},
		},
	})
}
