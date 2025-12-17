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
			wantErr: ErrInvalidSyntax,
		},
		{
			name:    "string: no int/colon - detected as int (first byte)",
			input:   "ilyasemantics",
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
			wantErr: ErrInvalidIntegerFormat,
		},
		{
			name:    "string: leading zero length",
			input:   "03:abc",
			wantErr: ErrInvalidIntegerFormat,
		},
		{
			name:    "string: leading zero zero",
			input:   "00:",
			wantErr: ErrInvalidIntegerFormat,
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
			name:    "int: missing terminator",
			input:   "i-e",
			wantErr: ErrInvalidIntegerFormat,
		},
		{
			name:    "int",
			input:   "li32ei25ee",
			wantVal: []interface{}{32, 25},
		},
		{
			name:  "dictionary",
			input: "d4:infod4:name5:b.txt6:lengthi1ee",
			wantVal: map[string]interface{}{
				"info": map[string]interface{}{"name": "b.txt", "length": 1},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("Testing for input: %s\n", tc.input)
			buf := bytes.NewBuffer([]byte(tc.input))

			data, err := NewDecoder(buf).Decode()

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
