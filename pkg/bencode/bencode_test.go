package bencode

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBencode_Decode(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		input   string
		wantErr error
		wantVal any
	}

	cases := []testCase{
		{
			name:    "string: invalid format",
			input:   "",
			wantErr: io.EOF,
		},
		{
			name:    "string: detected as int (first byte)",
			input:   "ilovesemantics",
			wantErr: ErrInvalidIntegerFormat,
		},
		{
			name:    "string: missing colon",
			input:   "5alice",
			wantErr: ErrInvalidStringFormat,
		},
		{
			name:    "string: invalid - length < string length",
			input:   "4:alicealice",
			wantErr: ErrTrailingDataLeft,
		},
		{
			name:    "string: invalid - length > string length",
			input:   "5:eggs",
			wantErr: io.ErrUnexpectedEOF,
		},
		{
			name:    "string: invalid - negative number",
			input:   "-5:eggs",
			wantErr: ErrInvalidStringFormat,
		},
		{
			name:    "string: leading zero length",
			input:   "03:abc",
			wantErr: ErrInvalidStringFormat,
		},
		{
			name:    "string: leading zero zero",
			input:   "00:",
			wantErr: ErrInvalidStringFormat,
		},
		{
			name:    "string: short",
			input:   "5:Alice",
			wantVal: "Alice",
		},
		{
			name:    "string: long",
			input:   "20:alicealicealicealice",
			wantVal: "alicealicealicealice",
		},
		{
			name:    "string: empty",
			input:   "0:",
			wantVal: "",
		},
		{
			name:    "int",
			input:   "i32e",
			wantVal: 32,
		},
		{
			name:    "int",
			input:   "i-32e",
			wantVal: -32,
		},
		{
			name:    "int",
			input:   "i0e",
			wantVal: 0,
		},
		{
			name:    "int",
			input:   "i20043e",
			wantVal: 20043,
		},
		{
			name:    "int",
			input:   "i1043002e",
			wantVal: 1043002,
		},
		{
			name:    "int - invalid",
			input:   "i-0e",
			wantErr: ErrInvalidIntegerFormat,
		},
		{
			name:    "int - invalid",
			input:   "i-23-4-2e",
			wantErr: ErrInvalidIntegerFormat,
		},
		{
			name:    "int - invalid",
			input:   "i1-3e",
			wantErr: ErrInvalidIntegerFormat,
		},
		{
			name:    "int - invalid",
			input:   "i03e",
			wantErr: ErrInvalidIntegerFormat,
		},
		{
			name:    "int: missing terminator",
			input:   "i32",
			wantErr: ErrInvalidIntegerFormat,
		},
		{
			name:    "int: missing integer",
			input:   "i-e",
			wantErr: ErrInvalidIntegerFormat,
		},
		{
			name:    "int - plus sign invalid",
			input:   "i+32e",
			wantErr: ErrInvalidIntegerFormat,
		},
		{
			name:    "int - non-digit characters",
			input:   "i12a3e",
			wantErr: ErrInvalidIntegerFormat,
		},
		{
			name:    "int - space inside number",
			input:   "i1 3e",
			wantErr: ErrInvalidIntegerFormat,
		},
		{
			name:    "list: strings and ints",
			input:   "l5:hello5:worldi123e3:abce",
			wantVal: []interface{}{"hello", "world", 123, "abc"},
		},
		{
			name:    "list: strings and ints",
			input:   "l5:helloi52ee",
			wantVal: []interface{}{"hello", 52},
		},
		{
			name:    "list: ints",
			input:   "li32ei25ee",
			wantVal: []interface{}{32, 25},
		},
		{
			name:    "dictionary: empty",
			input:   "de",
			wantVal: map[string]interface{}{},
		},
		{
			name:  "dictionary",
			input: "d4:infod4:name5:b.txt6:lengthi1eee",
			wantVal: map[string]interface{}{
				"info": map[string]interface{}{"name": "b.txt", "length": 1},
			},
		},
		{
			name:  "dictionary",
			input: "d6:client11:ArchTorrent7:versioni5ee",
			wantVal: map[string]interface{}{
				"client":  "ArchTorrent",
				"version": 5,
			},
		},
		{
			name:    "dictionary",
			input:   "di32e7:versioni5ee",
			wantVal: nil,
			wantErr: ErrDictKeyNotString,
		},
		{
			name:  "dictionary: torrent example",
			input: "d8:announce23:http://bt4.t-ru.org/ann13:announce-listll23:http://bt4.t-ru.org/annel31:http://retracker.local/announceee7:comment51:https://rutracker.org/forum/viewtopic.php?t=649613210:created by13:BitComet/2.0513:creation datei1709731450e8:encoding5:UTF-84:infod6:lengthi20028000e4:name52:Atkins Evan - GoLang for Machine Learning - 2024.PDF10:name.utf-852:Atkins Evan - GoLang for Machine Learning - 2024.PDF12:piece lengthi65536ee9:publisher13:rutracker.org13:publisher-url51:https://rutracker.org/forum/viewtopic.php?t=6496132e",
			wantVal: map[string]interface{}{
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("Testing for input: %s\n", tc.input)
			buf := bytes.NewBuffer([]byte(tc.input))

			var data interface{}
			err := NewDecoder(buf).Decode(&data)
			if tc.wantErr != nil {
				require.Error(t, err)
				require.Equal(t, err, tc.wantErr)
				require.Nil(t, data)
			} else {
				require.NoError(t, err)
				require.Equal(t, data, tc.wantVal)
			}
		})
	}
}

// func TestBencode_Encode(t *testing.T) {
// tc := `d8:announce41:http://bttracker.debian.org:6969/announce7:comment35:"Debian CD from cdimage.debian.org"13:creation datei1573903810e9:httpseedsl145:https://cdimage.debian.org/cdimage/release/10.2.0//srv/cdbuilder.debian.org/dst/deb-cd/weekly-builds/amd64/iso-cd/debian-10.2.0-amd64-netinst.iso145:https://cdimage.debian.org/cdimage/archive/10.2.0//srv/cdbuilder.debian.org/dst/deb-cd/weekly-builds/amd64/iso-cd/debian-10.2.0-amd64-netinst.isoe4:infod6:lengthi351272960e4:name31:debian-10.2.0-amd64-netinst.iso12:piece lengthi262144e6:pieces26800:�����PS�^�� (binary blob of the hashes of each piece)ee
// `

// 	type testCase struct {
// 		name  string
// 		input any
// 		want want
// 	}

// 	cases := []testCase{
// 		{
// 			name:  "byte string: string",
// 			input: "alice",
// 			wantVal: "5:alice",
// 		},
// 		{
// 			name:  "byte string: empty string",
// 			input: "",
// 			wantVal: "0:",
// 		},
// 		{
// 			name:  "int: basic int32",
// 			input: int32(2),
// 			wantVal: "i2e",
// 		},
// 		{
// 			name:  "int: negative int64",
// 			input: int64(-10),
// 			wantVal: "i-10e",
// 		},
// 		{
// 			name:  "int: uint8",
// 			input: uint(8),
// 			wantVal: "i8e",
// 		},
// 		{
// 			name:  "int: uint",
// 			input: uint(32),
// 			wantVal: "i32e",
// 		},
// 		{
// 			name:  "int: uint64",
// 			input: uint64(32),
// 			wantVal: "i32e",
// 		},
// 		{
// 			name:  "int: basic",
// 			input: 2,
// 			wantVal: "i2e",
// 		},
// 		{
// 			name:  "int: zero",
// 			input: 0,
// 			wantVal: "i0e",
// 		},
// 		{
// 			name:  "int: negative integer",
// 			input: -10,
// 			wantVal: "i-10e",
// 		},
// 		{
// 			name:  "list: strings only",
// 			input: []string{"spam", "age"},
// 			wantVal: "l4:spam3:agee",
// 		},
// 		{
// 			name:  "list: strings with ints",
// 			input: []string{"micheal", "jordan", "23"},
// 			wantVal: "l7:micheal6:jordan2:23e",
// 		},
// 		{
// 			name:  "list: ints",
// 			input: []int{1, 2, 3, 5, 8, 12},
// 			wantVal: "li1ei2ei3ei5ei8ei12ee",
// 		},
// 		{
// 			name:  "list: ints",
// 			input: []byte("yooooooo"),
// 			wantVal: "li1ei2ei3ei5ei8ei12ee",
// 		},
// 	}

// 	for _, tc := range cases {
// 		t.Run(tc.name, func(t *testing.T) {
// 			t.Parallel()

// 			value, err := Encode(tc.input)
// 			if tc.want.err != nil {
// 				require.Error(t, err)
// 				require.Equal(t, err, tc.want.err)
// 				require.Nil(t, value)
// 			} else {
// 				require.NoError(t, err)
// 				require.Equal(t, string(value), tc.want.value)
// 			}
// 		})
// 	}
// }
