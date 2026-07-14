package handlers

import (
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/metrics"
)

func TestRecordRecatFailure(t *testing.T) {
	before := testutil.ToFloat64(metrics.RecategorizeFailures.WithLabelValues("unit_test"))
	recordRecatFailure("unit_test", errors.New("boom"))
	after := testutil.ToFloat64(metrics.RecategorizeFailures.WithLabelValues("unit_test"))
	require.Equal(t, before+1, after)

	// err == nil não conta.
	recordRecatFailure("unit_test", nil)
	require.Equal(t, after, testutil.ToFloat64(metrics.RecategorizeFailures.WithLabelValues("unit_test")))
}
