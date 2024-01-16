package conecta

import (
	"math/rand"
	"time"
)

var (
	seed   = rand.NewSource(time.Now().UnixNano())
	random = rand.New(seed)
)

func GenerateRandomInt() int {
	return random.Int()
}
