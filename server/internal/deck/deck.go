package deck

import (
	"crypto/rand"
	"encoding/binary"
)

var ranks = []byte{'2', '3', '4', '5', '6', '7', '8', '9', 'T', 'J', 'Q', 'K', 'A'}
var suits = []byte{'s', 'h', 'd', 'c'}

// Standard 52-card deck represented as 2-byte strings like "As", "Td", "2c".
func NewDeck() []string {
	d := make([]string, 0, 52)
	for _, r := range ranks {
		for _, s := range suits {
			d = append(d, string([]byte{r, s}))
		}
	}
	return d
}

// Shuffle uses crypto/rand for Fisher-Yates. Not authenticated RNG but
// good enough for MVP; seed source is logged by the caller for audit.
func Shuffle(d []string) {
	for i := len(d) - 1; i > 0; i-- {
		j := cryptoIntn(i + 1)
		d[i], d[j] = d[j], d[i]
	}
}

func cryptoIntn(n int) int {
	if n <= 0 {
		return 0
	}
	var b [8]byte
	_, err := rand.Read(b[:])
	if err != nil {
		// extremely unlikely on darwin/linux; panic is fine for MVP.
		panic(err)
	}
	return int(binary.BigEndian.Uint64(b[:]) % uint64(n))
}
