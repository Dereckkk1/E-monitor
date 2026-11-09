package storage

import (
	"errors"
	"fmt"
	"testing"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/require"
)

func TestIsNotFound(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"embrulhado como em Client.Get", fmt.Errorf("storage: get x: %w", &s3types.NoSuchKey{}), true},
		{"direto", &s3types.NoSuchKey{}, true},
		// Bucket inexistente é configuração errada, não objeto que sumiu.
		{"bucket inexistente", fmt.Errorf("storage: get x: %w", &s3types.NoSuchBucket{}), false},
		{"outro erro", errors.New("conexão recusada"), false},
		{"sem erro", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, IsNotFound(c.err))
		})
	}
}
