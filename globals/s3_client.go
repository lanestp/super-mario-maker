package globals

import (
	"os"
	"strconv"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Credentials returns the credentials to reach S3 with. With an access
// key they are that key and its secret. Without one they are whatever
// role the machine has been given, as on an EC2 instance with an
// instance profile, so that no long lived key has to be stored
func S3Credentials(accessKey, accessSecret string) *credentials.Credentials {
	if strings.TrimSpace(accessKey) == "" {
		return credentials.NewIAM("")
	}

	return credentials.NewStaticV4(accessKey, accessSecret, "")
}

// S3ClientFromEnv makes the S3 client the PN_SMM_CONFIG_S3_ settings
// describe. It also reports whether PN_SMM_CONFIG_S3_SECURE was set to
// something understood. When it was not, TLS is used
func S3ClientFromEnv() (*minio.Client, bool, error) {
	secure, err := strconv.ParseBool(os.Getenv("PN_SMM_CONFIG_S3_SECURE"))
	secureWasSet := err == nil

	if !secureWasSet {
		secure = true
	}

	client, err := minio.New(os.Getenv("PN_SMM_CONFIG_S3_ENDPOINT"), &minio.Options{
		Creds:  S3Credentials(os.Getenv("PN_SMM_CONFIG_S3_ACCESS_KEY"), os.Getenv("PN_SMM_CONFIG_S3_ACCESS_SECRET")),
		Secure: secure,
		// * Left empty, the region is looked up from the bucket when needed
		Region: strings.TrimSpace(os.Getenv("PN_SMM_CONFIG_S3_REGION")),
	})

	return client, secureWasSet, err
}
