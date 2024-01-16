package conecta

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestObjectPool(t *testing.T) {
	pool := NewElementPool()
	element := &element{
		data:  "test",
		value: 1,
	}

	pool.Put(element)

	data := pool.Get()

	assert.Equal(t, data, element)
}
