package ccr

import (
	"testing"

	"github.com/containerd/containerd/v2/ccr/model"
	"github.com/stretchr/testify/assert"
)

func TestM(t *testing.T) {
	checkpoint, err := GetCheckpoint("sandbox", "container")
	assert.NoError(t, err)
	assert.Equal(t, &model.Checkpoint{}, checkpoint)

	checkpoint, err = CreateCheckpoint("sandbox", "container")
	assert.NoError(t, err)
	assert.NotNil(t, checkpoint)
	assert.Equal(t, 1, checkpoint.Round)
	assert.Equal(t, false, checkpoint.Committed)

	checkpoint, err = CommitCheckpoint(checkpoint.ID)
	assert.NoError(t, err)
	assert.NotNil(t, checkpoint)
	assert.Equal(t, true, checkpoint.Committed)

	checkpoint, err = GetCheckpoint(checkpoint.Sandbox, checkpoint.Container)
	assert.NoError(t, err)
	assert.NotNil(t, checkpoint)
	assert.Equal(t, true, checkpoint.Committed)
	assert.Equal(t, 1, checkpoint.Round)

	checkpoint, err = CreateCheckpoint("sandbox", "container")
	assert.NoError(t, err)
	assert.NotNil(t, checkpoint)
	assert.Equal(t, 2, checkpoint.Round)
	assert.Equal(t, false, checkpoint.Committed)

	checkpoint, err = CommitCheckpoint(checkpoint.ID)
	assert.NoError(t, err)
	assert.NotNil(t, checkpoint)
	assert.Equal(t, true, checkpoint.Committed)

	checkpoint, err = GetCheckpoint(checkpoint.Sandbox, checkpoint.Container)
	assert.NoError(t, err)
	assert.NotNil(t, checkpoint)
	assert.Equal(t, true, checkpoint.Committed)
	assert.Equal(t, 2, checkpoint.Round)

}
