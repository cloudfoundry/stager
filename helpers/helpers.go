package helpers

import (
	"encoding/json"

	"github.com/cloudfoundry-incubator/runtime-schema/cc_messages"
)

func BuildDockerStagingData(dockerImage string) (*json.RawMessage, error) {
	rawJsonBytes, err := json.Marshal(cc_messages.DockerStagingData{
		DockerImageUrl: dockerImage,
	})
	if err != nil {
		return nil, err
	}
	jsonRawMEssage := json.RawMessage(rawJsonBytes)
	return &jsonRawMEssage, nil
}
