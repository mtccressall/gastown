package testmode

import "testing"

func TestInitSuppressesInsideTestBinary(t *testing.T) {
	// The gate is only worth anything if it engages without any test opting in,
	// so assert the automatic path rather than a value this test set itself.
	if !WritesSuppressed() {
		t.Fatalf("WritesSuppressed() = false in a test binary (%s is unset or \"0\")", EnvSuppress)
	}
}

func TestWritesSuppressed(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", false},
		{"0", false},
		{"1", true},
		{"true", true},
	} {
		t.Setenv(EnvSuppress, tc.value)
		if got := WritesSuppressed(); got != tc.want {
			t.Errorf("%s=%q: WritesSuppressed() = %v, want %v", EnvSuppress, tc.value, got, tc.want)
		}
	}
}
