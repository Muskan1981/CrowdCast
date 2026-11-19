package presence

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPresenceService_Sparkline(t *testing.T) {
	svc := &Service{
		sparklineBars: []int{50, 60, 70},
	}

	svc.UpdateSparkline(120) // should clamp to 100
	assert.Equal(t, 100, svc.sparklineBars[len(svc.sparklineBars)-1])

	svc.UpdateSparkline(10) // should clamp to min 20
	assert.Equal(t, 20, svc.sparklineBars[len(svc.sparklineBars)-1])
}
