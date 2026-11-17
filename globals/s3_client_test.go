package globals

import (
	"testing"

	"github.com/minio/minio-go/v7/pkg/credentials"
)

func TestS3CredentialsUseTheKeyWhenThereIsOne(t *testing.T) {
	value, err := S3Credentials("key", "secret").GetWithContext(nil)
	if err != nil {
		t.Fatal(err)
	}

	if value.AccessKeyID != "key" || value.SecretAccessKey != "secret" || value.SignerType != credentials.SignatureV4 {
		t.Errorf("got key %q, secret %q, signer %v", value.AccessKeyID, value.SecretAccessKey, value.SignerType)
	}
}

// * Without a key the credentials must not be an empty static key, which
// * would sign requests as nobody. They come from the machine's role
func TestS3CredentialsUseTheMachinesRoleWithoutAKey(t *testing.T) {
	for _, accessKey := range []string{"", "   "} {
		static := credentials.NewStaticV4(accessKey, "", "")

		if got := S3Credentials(accessKey, ""); got == nil || *got == *static {
			t.Errorf("access key %q: got static credentials, want the machine's role", accessKey)
		}
	}
}
