package cmd

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestStale(t *testing.T) {
	now := time.Now()
	assert.False(t, stale(now.Add(-4*time.Minute), now, 5*time.Minute))
	assert.True(t, stale(now.Add(-5*time.Minute), now, 5*time.Minute))
}
