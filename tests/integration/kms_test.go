package integration_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKMSIntegration runs sub-tests sequentially against shared state.
// Each sub-test depends on the state left by the previous one; order matters.
func TestKMSIntegration(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()

	var keyID string  // created in CreateKey, reused by subsequent sub-tests
	var key2ID string // second key for UpdateAlias compatibility test

	t.Run("CreateKey", func(t *testing.T) {
		out, err := clients.kms.CreateKey(ctx, &awskms.CreateKeyInput{
			Description: aws.String("integration test key"),
		})
		require.NoError(t, err)
		require.NotNil(t, out.KeyMetadata)
		assert.NotEmpty(t, out.KeyMetadata.KeyId)
		assert.Equal(t, "integration test key", aws.ToString(out.KeyMetadata.Description))
		assert.Equal(t, types.KeySpecSymmetricDefault, out.KeyMetadata.KeySpec)
		assert.Equal(t, types.KeyUsageTypeEncryptDecrypt, out.KeyMetadata.KeyUsage)
		assert.Equal(t, types.KeyStateEnabled, out.KeyMetadata.KeyState)
		assert.True(t, out.KeyMetadata.Enabled)
		keyID = aws.ToString(out.KeyMetadata.KeyId)
	})

	t.Run("CreateKey_second", func(t *testing.T) {
		out, err := clients.kms.CreateKey(ctx, &awskms.CreateKeyInput{
			Description: aws.String("integration test key 2"),
		})
		require.NoError(t, err)
		require.NotNil(t, out.KeyMetadata)
		key2ID = aws.ToString(out.KeyMetadata.KeyId)
	})

	t.Run("DescribeKey", func(t *testing.T) {
		out, err := clients.kms.DescribeKey(ctx, &awskms.DescribeKeyInput{
			KeyId: aws.String(keyID),
		})
		require.NoError(t, err)
		assert.Equal(t, keyID, aws.ToString(out.KeyMetadata.KeyId))
		assert.Equal(t, "integration test key", aws.ToString(out.KeyMetadata.Description))
	})

	t.Run("ListKeys", func(t *testing.T) {
		out, err := clients.kms.ListKeys(ctx, &awskms.ListKeysInput{})
		require.NoError(t, err)
		var found bool
		for _, k := range out.Keys {
			if aws.ToString(k.KeyId) == keyID {
				found = true
				break
			}
		}
		assert.True(t, found, "created key should appear in ListKeys")
	})

	t.Run("GetKeyPolicy", func(t *testing.T) {
		out, err := clients.kms.GetKeyPolicy(ctx, &awskms.GetKeyPolicyInput{
			KeyId:      aws.String(keyID),
			PolicyName: aws.String("default"),
		})
		require.NoError(t, err)
		assert.NotEmpty(t, aws.ToString(out.Policy))
		assert.Equal(t, "default", aws.ToString(out.PolicyName))
	})

	t.Run("PutKeyPolicy", func(t *testing.T) {
		policy := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::000000000000:root"},"Action":"kms:*","Resource":"*"}]}`
		_, err := clients.kms.PutKeyPolicy(ctx, &awskms.PutKeyPolicyInput{
			KeyId:      aws.String(keyID),
			PolicyName: aws.String("default"),
			Policy:     aws.String(policy),
		})
		require.NoError(t, err)

		out, err := clients.kms.GetKeyPolicy(ctx, &awskms.GetKeyPolicyInput{
			KeyId:      aws.String(keyID),
			PolicyName: aws.String("default"),
		})
		require.NoError(t, err)
		assert.Equal(t, policy, aws.ToString(out.Policy))
	})

	t.Run("Encrypt_Decrypt_roundtrip", func(t *testing.T) {
		plaintext := []byte("hello kumolo KMS")

		encOut, err := clients.kms.Encrypt(ctx, &awskms.EncryptInput{
			KeyId:     aws.String(keyID),
			Plaintext: plaintext,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, encOut.CiphertextBlob)

		decOut, err := clients.kms.Decrypt(ctx, &awskms.DecryptInput{
			CiphertextBlob: encOut.CiphertextBlob,
		})
		require.NoError(t, err)
		assert.Equal(t, plaintext, decOut.Plaintext)
	})

	t.Run("Encrypt_Decrypt_with_context", func(t *testing.T) {
		plaintext := []byte("context-bound plaintext")
		encCtx := map[string]string{"env": "test", "app": "kumolo"}

		encOut, err := clients.kms.Encrypt(ctx, &awskms.EncryptInput{
			KeyId:             aws.String(keyID),
			Plaintext:         plaintext,
			EncryptionContext: encCtx,
		})
		require.NoError(t, err)

		decOut, err := clients.kms.Decrypt(ctx, &awskms.DecryptInput{
			CiphertextBlob:    encOut.CiphertextBlob,
			EncryptionContext: encCtx,
		})
		require.NoError(t, err)
		assert.Equal(t, plaintext, decOut.Plaintext)

		_, err = clients.kms.Decrypt(ctx, &awskms.DecryptInput{
			CiphertextBlob:    encOut.CiphertextBlob,
			EncryptionContext: map[string]string{"env": "wrong"},
		})
		assert.Equal(t, "InvalidCiphertextException", apiErrorCode(err))
	})

	t.Run("GenerateDataKey", func(t *testing.T) {
		out, err := clients.kms.GenerateDataKey(ctx, &awskms.GenerateDataKeyInput{
			KeyId:   aws.String(keyID),
			KeySpec: types.DataKeySpecAes256,
		})
		require.NoError(t, err)
		assert.Len(t, out.Plaintext, 32)
		assert.NotEmpty(t, out.CiphertextBlob)

		// Decrypt the wrapped data key to verify it matches the plaintext.
		decOut, err := clients.kms.Decrypt(ctx, &awskms.DecryptInput{
			CiphertextBlob: out.CiphertextBlob,
		})
		require.NoError(t, err)
		assert.Equal(t, out.Plaintext, decOut.Plaintext)
	})

	t.Run("GenerateDataKeyWithoutPlaintext", func(t *testing.T) {
		out, err := clients.kms.GenerateDataKeyWithoutPlaintext(
			ctx,
			&awskms.GenerateDataKeyWithoutPlaintextInput{
				KeyId:   aws.String(keyID),
				KeySpec: types.DataKeySpecAes128,
			},
		)
		require.NoError(t, err)
		assert.NotEmpty(t, out.CiphertextBlob)

		// Decrypt the wrapped data key and verify it is 16 bytes (AES-128).
		decOut, err := clients.kms.Decrypt(ctx, &awskms.DecryptInput{
			CiphertextBlob: out.CiphertextBlob,
		})
		require.NoError(t, err)
		assert.Len(t, decOut.Plaintext, 16)
	})

	t.Run("CreateAlias", func(t *testing.T) {
		_, err := clients.kms.CreateAlias(ctx, &awskms.CreateAliasInput{
			AliasName:   aws.String("alias/integration-test"),
			TargetKeyId: aws.String(keyID),
		})
		require.NoError(t, err)
	})

	t.Run("ListAliases", func(t *testing.T) {
		out, err := clients.kms.ListAliases(ctx, &awskms.ListAliasesInput{
			KeyId: aws.String(keyID),
		})
		require.NoError(t, err)
		var found bool
		for _, a := range out.Aliases {
			if aws.ToString(a.AliasName) == "alias/integration-test" {
				found = true
				break
			}
		}
		assert.True(t, found, "created alias should appear in ListAliases")
	})

	t.Run("DescribeKey_via_alias", func(t *testing.T) {
		out, err := clients.kms.DescribeKey(ctx, &awskms.DescribeKeyInput{
			KeyId: aws.String("alias/integration-test"),
		})
		require.NoError(t, err)
		assert.Equal(t, keyID, aws.ToString(out.KeyMetadata.KeyId))
	})

	t.Run("UpdateAlias", func(t *testing.T) {
		_, err := clients.kms.UpdateAlias(ctx, &awskms.UpdateAliasInput{
			AliasName:   aws.String("alias/integration-test"),
			TargetKeyId: aws.String(key2ID),
		})
		require.NoError(t, err)

		out, err := clients.kms.DescribeKey(ctx, &awskms.DescribeKeyInput{
			KeyId: aws.String("alias/integration-test"),
		})
		require.NoError(t, err)
		assert.Equal(t, key2ID, aws.ToString(out.KeyMetadata.KeyId))
	})

	t.Run("DeleteAlias", func(t *testing.T) {
		_, err := clients.kms.DeleteAlias(ctx, &awskms.DeleteAliasInput{
			AliasName: aws.String("alias/integration-test"),
		})
		require.NoError(t, err)

		_, err = clients.kms.DescribeKey(ctx, &awskms.DescribeKeyInput{
			KeyId: aws.String("alias/integration-test"),
		})
		assert.Equal(t, "NotFoundException", apiErrorCode(err))
	})

	t.Run("DisableKey", func(t *testing.T) {
		_, err := clients.kms.DisableKey(ctx, &awskms.DisableKeyInput{
			KeyId: aws.String(keyID),
		})
		require.NoError(t, err)

		out, err := clients.kms.DescribeKey(ctx, &awskms.DescribeKeyInput{
			KeyId: aws.String(keyID),
		})
		require.NoError(t, err)
		assert.Equal(t, types.KeyStateDisabled, out.KeyMetadata.KeyState)
		assert.False(t, out.KeyMetadata.Enabled)

		_, err = clients.kms.Encrypt(ctx, &awskms.EncryptInput{
			KeyId:     aws.String(keyID),
			Plaintext: []byte("should fail"),
		})
		assert.Equal(t, "DisabledException", apiErrorCode(err))
	})

	t.Run("EnableKey", func(t *testing.T) {
		_, err := clients.kms.EnableKey(ctx, &awskms.EnableKeyInput{
			KeyId: aws.String(keyID),
		})
		require.NoError(t, err)

		out, err := clients.kms.DescribeKey(ctx, &awskms.DescribeKeyInput{
			KeyId: aws.String(keyID),
		})
		require.NoError(t, err)
		assert.Equal(t, types.KeyStateEnabled, out.KeyMetadata.KeyState)
		assert.True(t, out.KeyMetadata.Enabled)
	})

	t.Run("EnableKeyRotation", func(t *testing.T) {
		_, err := clients.kms.EnableKeyRotation(ctx, &awskms.EnableKeyRotationInput{
			KeyId:                aws.String(keyID),
			RotationPeriodInDays: aws.Int32(90),
		})
		require.NoError(t, err)
	})

	t.Run("GetKeyRotationStatus", func(t *testing.T) {
		out, err := clients.kms.GetKeyRotationStatus(ctx, &awskms.GetKeyRotationStatusInput{
			KeyId: aws.String(keyID),
		})
		require.NoError(t, err)
		assert.True(t, out.KeyRotationEnabled)
		assert.Equal(t, int32(90), aws.ToInt32(out.RotationPeriodInDays))
	})

	t.Run("DisableKeyRotation", func(t *testing.T) {
		_, err := clients.kms.DisableKeyRotation(ctx, &awskms.DisableKeyRotationInput{
			KeyId: aws.String(keyID),
		})
		require.NoError(t, err)

		out, err := clients.kms.GetKeyRotationStatus(ctx, &awskms.GetKeyRotationStatusInput{
			KeyId: aws.String(keyID),
		})
		require.NoError(t, err)
		assert.False(t, out.KeyRotationEnabled)
	})

	t.Run("ScheduleKeyDeletion", func(t *testing.T) {
		out, err := clients.kms.ScheduleKeyDeletion(ctx, &awskms.ScheduleKeyDeletionInput{
			KeyId:               aws.String(keyID),
			PendingWindowInDays: aws.Int32(7),
		})
		require.NoError(t, err)
		assert.Equal(t, types.KeyStatePendingDeletion, out.KeyState)
		assert.NotNil(t, out.DeletionDate)

		_, err = clients.kms.Encrypt(ctx, &awskms.EncryptInput{
			KeyId:     aws.String(keyID),
			Plaintext: []byte("should fail"),
		})
		assert.Equal(t, "KMSInvalidStateException", apiErrorCode(err))
	})

	t.Run("CancelKeyDeletion", func(t *testing.T) {
		out, err := clients.kms.CancelKeyDeletion(ctx, &awskms.CancelKeyDeletionInput{
			KeyId: aws.String(keyID),
		})
		require.NoError(t, err)
		assert.NotEmpty(t, aws.ToString(out.KeyId))

		meta, err := clients.kms.DescribeKey(ctx, &awskms.DescribeKeyInput{
			KeyId: aws.String(keyID),
		})
		require.NoError(t, err)
		assert.Equal(t, types.KeyStateDisabled, meta.KeyMetadata.KeyState)
	})
}
