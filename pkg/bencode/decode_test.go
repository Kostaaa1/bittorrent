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
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			t.Logf("Testing for input: %s\n", tc.input)

			buf := bytes.NewBuffer([]byte(tc.input))

			var got T
			err := NewDecoder(buf).Decode(&got)

			if tc.err != nil {
				require.Error(t, err)
				require.Equal(t, err, tc.err)
				require.Zero(t, got)
			} else {
				require.NoError(t, err)
				require.True(t, reflect.DeepEqual(got, tc.want))
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
	})

	run(t, []testCase[int]{
		{
			name:  "int: negative 0",
			input: "i-023e",
			err:   ErrInvalidIntegerFormat,
		},
		{
			name:  "int: negative 0",
			input: "i-0e",
			err:   ErrInvalidIntegerFormat,
		},
		{
			name:  "int: invalid format",
			input: "i-23-4-2e",
			err:   ErrInvalidIntegerFormat,
		},
		{
			name:  "int: invalid format",
			input: "i1-3e",
			err:   ErrInvalidIntegerFormat,
		},
		{
			name:  "int: invalid format",
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
			name:  "int: plus sign invalid",
			input: "i+32e",
			err:   ErrInvalidIntegerFormat,
		},
		{
			name:  "int: non-digit characters",
			input: "i12a3e",
			err:   ErrInvalidIntegerFormat,
		},
		{
			name:  "int: space inside number",
			input: "i1 3e",
			err:   ErrInvalidIntegerFormat,
		},
	})

	run(t, []testCase[int64]{
		{
			name:  "int64: positive",
			input: "i9223372036854775807e",
			want:  9223372036854775807,
		},
		{
			name:  "int64: negative",
			input: "i-9223372036854775808e",
			want:  -9223372036854775808,
		},
		{
			name:  "int64: zero",
			input: "i0e",
			want:  0,
		},
	})

	run(t, []testCase[int32]{
		{
			name:  "int32: max",
			input: "i2147483647e",
			want:  2147483647,
		},
		{
			name:  "int32: min",
			input: "i-2147483648e",
			want:  -2147483648,
		},
	})

	run(t, []testCase[uint]{
		{
			name:  "uint: positive",
			input: "i42e",
			want:  42,
		},
		{
			name:  "uint: zero",
			input: "i0e",
			want:  0,
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
		{
			name:  "list: empty",
			input: "le",
			want:  []interface{}{},
		},
		{
			name:  "list: single element",
			input: "l3:fooe",
			want:  []interface{}{"foo"},
		},
		{
			name:  "list: nested list",
			input: "ll3:foo3:baree",
			want:  []interface{}{[]interface{}{"foo", "bar"}},
		},
		{
			name:  "list: mixed nested",
			input: "l3:fooli1ei2ei3ee5:helloe",
			want:  []interface{}{"foo", []interface{}{1, 2, 3}, "hello"},
		},
		{
			name:  "list: deeply nested",
			input: "llli42eeee",
			want:  []interface{}{[]interface{}{[]interface{}{42}}},
		},
		{
			name:  "list: containing dictionary",
			input: "ld3:foo3:baree",
			want:  []interface{}{map[string]interface{}{"foo": "bar"}},
		},
		{
			name:  "list: missing terminator",
			input: "l3:foo",
			err:   io.EOF,
		},
	})
}

func TestDecode_ListTyped(t *testing.T) {
	run(t, []testCase[[]string]{
		{
			name:  "list of strings",
			input: "l5:hello5:world3:fooe",
			want:  []string{"hello", "world", "foo"},
		},
		{
			name:  "list of strings: empty",
			input: "le",
			want:  []string{},
		},
		{
			name:  "list of strings: single",
			input: "l4:teste",
			want:  []string{"test"},
		},
	})

	run(t, []testCase[[]int]{
		{
			name:  "list of ints",
			input: "li1ei2ei3ei4ei5ee",
			want:  []int{1, 2, 3, 4, 5},
		},
		{
			name:  "list of ints: empty",
			input: "le",
			want:  []int{},
		},
		{
			name:  "list of ints: negative",
			input: "li-10ei0ei10ee",
			want:  []int{-10, 0, 10},
		},
	})

	run(t, []testCase[[][]string]{
		{
			name:  "nested list of strings",
			input: "ll3:foo3:barel3:baz3:quxee",
			want:  [][]string{{"foo", "bar"}, {"baz", "qux"}},
		},
		{
			name:  "nested list of strings: empty inner",
			input: "llel3:fooel3:bar3:bazee",
			want:  [][]string{{}, {"foo"}, {"bar", "baz"}},
		},
	})
}

func TestDecode_ByteSlice(t *testing.T) {
	run(t, []testCase[[]byte]{
		{
			name:  "byte slice: simple",
			input: "5:hello",
			want:  []byte("hello"),
		},
		{
			name:  "byte slice: empty",
			input: "0:",
			want:  []byte(""),
		},
		{
			name:  "byte slice: binary data",
			input: "4:\x00\x01\x02\x03",
			want:  []byte{0x00, 0x01, 0x02, 0x03},
		},
		{
			name:  "byte slice: sha1 hash simulation",
			input: "20:01234567890123456789",
			want:  []byte("01234567890123456789"),
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
			input: "d6:client11:ArchTorrent7:version5:alicee",
			want: map[string]interface{}{
				"client":  "ArchTorrent",
				"version": "alice",
			},
		},
		{
			name:  "dictionary",
			input: "di32e7:versioni5ee",
			want:  nil,
			err:   ErrInvalidStringFormat,
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

	run(t, []testCase[map[string]string]{
		{
			name:  "map[string]string: simple",
			input: "d4:name5:Alice4:city6:Parisie",
			want:  map[string]string{"name": "Alice", "city": "Parisi"},
		},
		{
			name:  "map[string]string: empty",
			input: "de",
			want:  map[string]string{},
		},
		{
			name:  "map[string]string: single key",
			input: "d3:foo3:bare",
			want:  map[string]string{"foo": "bar"},
		},
	})

	run(t, []testCase[map[string]int]{
		{
			name:  "map[string]int: simple",
			input: "d3:agei25e5:scorei100ee",
			want:  map[string]int{"age": 25, "score": 100},
		},
		{
			name:  "map[string]int: empty",
			input: "de",
			want:  map[string]int{},
		},
		{
			name:  "map[string]int: negative values",
			input: "d5:debiti-500e7:balancei1000ee",
			want:  map[string]int{"debit": -500, "balance": 1000},
		},
	})

	run(t, []testCase[map[string][]string]{
		{
			name:  "map[string][]string: simple",
			input: "d4:tagsl3:foo3:bar3:bazee",
			want:  map[string][]string{"tags": {"foo", "bar", "baz"}},
		},
		{
			name:  "map[string][]string: multiple keys",
			input: "d6:colorsl3:red5:greene5:sizesl5:small6:mediumee",
			want:  map[string][]string{"colors": {"red", "green"}, "sizes": {"small", "medium"}},
		},
	})
}

type Torrent struct {
	Announce     string     `bencode:"announce"`
	AnnounceList [][]string `bencode:"announce-list,omitempty"`
	Comment      string     `bencode:"comment,omitempty"`
	CreatedBy    string     `bencode:"created by,omitempty"`
	CreationDate int64      `bencode:"creation date,omitempty"`
	Encoding     string     `bencode:"encoding,omitempty"`
	Info         InfoDict   `bencode:"info"`
}

type InfoDict struct {
	// Length      int64      `bencode:"length"`
	Name        string     `bencode:"name"`
	PieceLength int64      `bencode:"piece length"`
	Pieces      []byte     `bencode:"pieces"`
	Files       []FileInfo `bencode:"files,omitempty"`
}

type FileInfo struct {
	Length int64    `bencode:"length"`
	Path   []string `bencode:"path"`
}

type SimpleStruct struct {
	Name  string `bencode:"name"`
	Value int    `bencode:"value"`
}

func TestDecode_SimpleStruct(t *testing.T) {

	run(t, []testCase[SimpleStruct]{
		{
			name:  "simple struct",
			input: "d4:name5:Alice5:valuei42ee",
			want:  SimpleStruct{Name: "Alice", Value: 42},
		},
		{
			name:  "simple struct: empty strings",
			input: "d4:name0:5:valuei0ee",
			want:  SimpleStruct{Name: "", Value: 0},
		},
		{
			name:  "simple struct: negative value",
			input: "d4:name3:Bob5:valuei-100ee",
			want:  SimpleStruct{Name: "Bob", Value: -100},
		},
	})
}

type StructWithOptional struct {
	Required string `bencode:"required"`
	Optional string `bencode:"optional,omitempty"`
	Count    int    `bencode:"count,omitempty"`
}

func TestDecode_StructWithOptional(t *testing.T) {
	run(t, []testCase[StructWithOptional]{
		{
			name:  "struct with all fields",
			input: "d5:counti10e8:optional5:extra8:required4:teste",
			want:  StructWithOptional{Required: "test", Optional: "extra", Count: 10},
		},
		{
			name:  "struct with only required",
			input: "d8:required5:helloe",
			want:  StructWithOptional{Required: "hello", Optional: "", Count: 0},
		},
		{
			name:  "struct with unknown fields ignored",
			input: "d8:required4:test7:unknown5:valuee",
			want:  StructWithOptional{Required: "test", Optional: "", Count: 0},
		},
	})
}

type NestedStruct struct {
	Outer string       `bencode:"outer"`
	Inner SimpleStruct `bencode:"inner"`
}

func TestDecode_NestedStruct(t *testing.T) {
	run(t, []testCase[NestedStruct]{
		{
			name:  "nested struct",
			input: "d5:innerd4:name3:Bob5:valuei99ee5:outer5:Helloe",
			want: NestedStruct{
				Outer: "Hello",
				Inner: SimpleStruct{Name: "Bob", Value: 99},
			},
		},
		{
			name:  "nested struct: empty inner",
			input: "d5:innerd4:name0:5:valuei0ee5:outer4:teste",
			want: NestedStruct{
				Outer: "test",
				Inner: SimpleStruct{Name: "", Value: 0},
			},
		},
	})
}

type StructWithSlice struct {
	Name  string   `bencode:"name"`
	Items []string `bencode:"items"`
}

func TestDecode_StructWithSlice(t *testing.T) {
	run(t, []testCase[StructWithSlice]{
		{
			name:  "struct with slice",
			input: "d5:itemsl3:one3:two5:threee4:name4:teste",
			want:  StructWithSlice{Name: "test", Items: []string{"one", "two", "three"}},
		},
		{
			name:  "struct with empty slice",
			input: "d5:itemsle4:name4:teste",
			want:  StructWithSlice{Name: "test", Items: []string{}},
		},
	})
}

type StructWithByteSlice struct {
	ID   string `bencode:"id"`
	Data []byte `bencode:"data"`
}

func TestDecode_StructWithByteSlice(t *testing.T) {
	run(t, []testCase[StructWithByteSlice]{
		{
			name:  "struct with byte slice",
			input: "d4:data10:01234567892:id4:teste",
			want:  StructWithByteSlice{ID: "test", Data: []byte("0123456789")},
		},
		{
			name:  "struct with binary data",
			input: "d4:data4:\x00\x01\x02\x032:id3:bine",
			want:  StructWithByteSlice{ID: "bin", Data: []byte{0x00, 0x01, 0x02, 0x03}},
		},
	})
}

func TestDecode_DictionaryToStruct(t *testing.T) {
	run(t, []testCase[Torrent]{
		{
			name:  "struct",
			input: "d8:announce23:http://bt3.t-ru.org/ann13:announce-listll23:http://bt3.t-ru.org/annel31:http://retracker.local/announceee7:comment51:https://rutracker.org/forum/viewtopic.php?t=664106710:created by13:uTorrent/323013:creation datei1738724920e8:encoding5:UTF-84:infod5:filesld6:lengthi5845297e4:pathl57:Guha Rehan - Machine Learning Interview Guide - 2025.epubeed6:lengthi1990163e4:pathl56:Guha Rehan - Machine Learning Interview Guide - 2025.pdfeee4:name52:Guha Rehan - Machine Learning Interview Guide - 202512:piece lengthi16384e6:pieces20:xxxxxxxxxxxxxxxxxxxxe9:publisher13:rutracker.org13:publisher-url51:https://rutracker.org/forum/viewtopic.php?t=6641067e",
			want: Torrent{
				Announce: "http://bt3.t-ru.org/ann",
				AnnounceList: [][]string{
					{"http://bt3.t-ru.org/ann"},
					{"http://retracker.local/announce"},
				},
				Comment:      "https://rutracker.org/forum/viewtopic.php?t=6641067",
				CreatedBy:    "uTorrent/3230",
				CreationDate: 1738724920,
				Encoding:     "UTF-8",
				Info: InfoDict{
					Name:        "Guha Rehan - Machine Learning Interview Guide - 2025",
					PieceLength: 16384,
					Pieces:      []byte("xxxxxxxxxxxxxxxxxxxx"),
					Files: []FileInfo{
						{
							Length: 5845297,
							Path:   []string{"Guha Rehan - Machine Learning Interview Guide - 2025.epub"},
						},
						{
							Length: 1990163,
							Path:   []string{"Guha Rehan - Machine Learning Interview Guide - 2025.pdf"},
						},
					},
				},
			},
			err: nil,
		},
		{
			name:  "struct: single file torrent",
			input: "d8:announce20:http://example.com/a4:infod6:lengthi1024e4:name8:file.txt12:piece lengthi512e6:pieces20:abcdefghij0123456789ee",
			want: Torrent{
				Announce: "http://example.com/a",
				Info: InfoDict{
					// Length:      1024,
					Name:        "file.txt",
					PieceLength: 512,
					Pieces:      []byte("abcdefghij0123456789"),
				},
			},
			err: nil,
		},
		{
			name:  "struct: minimal torrent",
			input: "d8:announce15:http://test.com4:infod4:name4:test12:piece lengthi256e6:pieces0:ee",
			want: Torrent{
				Announce: "http://test.com",
				Info: InfoDict{
					Name:        "test",
					PieceLength: 256,
					Pieces:      []byte(""),
				},
			},
			err: nil,
		},
	})
}
