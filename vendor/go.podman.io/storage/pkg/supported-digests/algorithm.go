package supporteddigests

import (
	"fmt"

	"github.com/opencontainers/go-digest"
	"github.com/sirupsen/logrus"
)

var digestAlgorithm = digest.Canonical // Default to SHA256

// Get returns the current digest algorithm
func Get() digest.Algorithm {
	return digestAlgorithm
}

// Set sets the digest algorithm
func Set(algorithm digest.Algorithm) error {
	// Validate the digest type
	switch algorithm {
	case digest.SHA256, digest.SHA512:
		logrus.Debugf("SetDigestAlgorithm: Setting digest algorithm to %s", algorithm.String())
		digestAlgorithm = algorithm
		return nil
	case "":
		logrus.Debugf("SetDigestAlgorithm: Setting digest algorithm to default %s", digest.Canonical.String())
		digestAlgorithm = digest.Canonical // Default to sha256
		return nil
	default:
		return fmt.Errorf("unsupported digest algorithm: %q", algorithm)
	}
}
