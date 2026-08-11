package main

import "slices"

type releasedProtocolGenerationMatrix struct {
	Protocol      string                        `json:"protocol"`
	PeerKind      string                        `json:"peer_kind"`
	Directions    []releasedGenerationDirection `json:"directions"`
	RequiredCases []string                      `json:"required_cases"`
}

func (matrix releasedProtocolGenerationMatrix) Equal(other releasedProtocolGenerationMatrix) bool {
	return matrix.Protocol == other.Protocol && matrix.PeerKind == other.PeerKind &&
		slices.Equal(matrix.Directions, other.Directions) && slices.Equal(matrix.RequiredCases, other.RequiredCases)
}
