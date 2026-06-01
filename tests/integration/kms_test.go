package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const kmsTestTimeout = 30 * time.Second

func kmsCreateKey(ctx context.Context, t *testing.T, clients testClients, desc string) string {
	t.Helper()
	out, err := clients.kms.CreateKey(ctx, &awskms.CreateKeyInput{
		Description: aws.String(desc),
	})
	require.NoError(t, err)
	return aws.ToString(out.KeyMetadata.KeyId)
}

func TestKMSKeyCreation(t *testing.T) {
	clients := newTestClients(t)
	ctx, cancel := context.WithTimeout(context.Background(), kmsTestTimeout)
	defer cancel()

	t.Run("creates a symmetric encrypt/decrypt key with the given description", func(t *testing.T) {
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
	})
}

func TestKMSKeyDescribeAndList(t *testing.T) {
	clients := newTestClients(t)
	ctx, cancel := context.WithTimeout(context.Background(), kmsTestTimeout)
	defer cancel()

	keyID := kmsCreateKey(ctx, t, clients, "integration test key")

	t.Run("returns metadata for a known key", func(t *testing.T) {
		out, err := clients.kms.DescribeKey(ctx, &awskms.DescribeKeyInput{
			KeyId: aws.String(keyID),
		})
		require.NoError(t, err)
		assert.Equal(t, keyID, aws.ToString(out.KeyMetadata.KeyId))
		assert.Equal(t, "integration test key", aws.ToString(out.KeyMetadata.Description))
	})

	t.Run("includes the created key in list results", func(t *testing.T) {
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
}

func TestKMSKeyPolicy(t *testing.T) {
	clients := newTestClients(t)
	ctx, cancel := context.WithTimeout(context.Background(), kmsTestTimeout)
	defer cancel()

	keyID := kmsCreateKey(ctx, t, clients, "policy test key")

	t.Run("returns the default policy for a new key", func(t *testing.T) {
		out, err := clients.kms.GetKeyPolicy(ctx, &awskms.GetKeyPolicyInput{
			KeyId:      aws.String(keyID),
			PolicyName: aws.String("default"),
		})
		require.NoError(t, err)
		assert.NotEmpty(t, aws.ToString(out.Policy))
		assert.Equal(t, "default", aws.ToString(out.PolicyName))
	})

	t.Run("reflects the updated policy after PutKeyPolicy", func(t *testing.T) {
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

		var want, got any
		require.NoError(t, json.Unmarshal([]byte(policy), &want))
		require.NoError(t, json.Unmarshal([]byte(aws.ToString(out.Policy)), &got))
		assert.Equal(t, want, got)
	})
}

func TestKMSEncryptDecrypt(t *testing.T) {
	clients := newTestClients(t)
	ctx, cancel := context.WithTimeout(context.Background(), kmsTestTimeout)
	defer cancel()

	keyID := kmsCreateKey(ctx, t, clients, "encrypt test key")

	t.Run("decrypted ciphertext matches the original plaintext", func(t *testing.T) {
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

	t.Run("decrypts successfully with matching encryption context", func(t *testing.T) {
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
	})

	t.Run(
		"returns InvalidCiphertextException when encryption context does not match",
		func(t *testing.T) {
			plaintext := []byte("context-bound plaintext")
			encCtx := map[string]string{"env": "test", "app": "kumolo"}

			encOut, err := clients.kms.Encrypt(ctx, &awskms.EncryptInput{
				KeyId:             aws.String(keyID),
				Plaintext:         plaintext,
				EncryptionContext: encCtx,
			})
			require.NoError(t, err)

			_, err = clients.kms.Decrypt(ctx, &awskms.DecryptInput{
				CiphertextBlob:    encOut.CiphertextBlob,
				EncryptionContext: map[string]string{"env": "wrong"},
			})
			assert.Equal(t, "InvalidCiphertextException", apiErrorCode(err))
		},
	)
}

func TestKMSDataKeys(t *testing.T) {
	clients := newTestClients(t)
	ctx, cancel := context.WithTimeout(context.Background(), kmsTestTimeout)
	defer cancel()

	keyID := kmsCreateKey(ctx, t, clients, "data key test key")

	t.Run(
		"generates a 256-bit data key and wrapped ciphertext that decrypts to the same key",
		func(t *testing.T) {
			out, err := clients.kms.GenerateDataKey(ctx, &awskms.GenerateDataKeyInput{
				KeyId:   aws.String(keyID),
				KeySpec: types.DataKeySpecAes256,
			})
			require.NoError(t, err)
			assert.Len(t, out.Plaintext, 32)
			assert.NotEmpty(t, out.CiphertextBlob)

			decOut, err := clients.kms.Decrypt(ctx, &awskms.DecryptInput{
				CiphertextBlob: out.CiphertextBlob,
			})
			require.NoError(t, err)
			assert.Equal(t, out.Plaintext, decOut.Plaintext)
		},
	)

	t.Run("generates a 128-bit wrapped data key without returning plaintext", func(t *testing.T) {
		out, err := clients.kms.GenerateDataKeyWithoutPlaintext(
			ctx,
			&awskms.GenerateDataKeyWithoutPlaintextInput{
				KeyId:   aws.String(keyID),
				KeySpec: types.DataKeySpecAes128,
			},
		)
		require.NoError(t, err)
		assert.NotEmpty(t, out.CiphertextBlob)

		decOut, err := clients.kms.Decrypt(ctx, &awskms.DecryptInput{
			CiphertextBlob: out.CiphertextBlob,
		})
		require.NoError(t, err)
		assert.Len(t, decOut.Plaintext, 16)
	})
}

func TestKMSAliasOperations(t *testing.T) {
	clients := newTestClients(t)
	ctx, cancel := context.WithTimeout(context.Background(), kmsTestTimeout)
	defer cancel()

	keyID := kmsCreateKey(ctx, t, clients, "alias test key 1")
	key2ID := kmsCreateKey(ctx, t, clients, "alias test key 2")

	// Subtests below are sequential: each depends on state set by the previous one.

	t.Run("creates an alias and the alias appears in ListAliases for the key", func(t *testing.T) {
		_, err := clients.kms.CreateAlias(ctx, &awskms.CreateAliasInput{
			AliasName:   aws.String("alias/integration-test"),
			TargetKeyId: aws.String(keyID),
		})
		require.NoError(t, err)

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

	t.Run("DescribeKey resolves the target key when called with an alias", func(t *testing.T) {
		out, err := clients.kms.DescribeKey(ctx, &awskms.DescribeKeyInput{
			KeyId: aws.String("alias/integration-test"),
		})
		require.NoError(t, err)
		assert.Equal(t, keyID, aws.ToString(out.KeyMetadata.KeyId))
	})

	t.Run("UpdateAlias retargets the alias to a different key", func(t *testing.T) {
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

	t.Run("DescribeKey returns NotFoundException after the alias is deleted", func(t *testing.T) {
		_, err := clients.kms.DeleteAlias(ctx, &awskms.DeleteAliasInput{
			AliasName: aws.String("alias/integration-test"),
		})
		require.NoError(t, err)

		_, err = clients.kms.DescribeKey(ctx, &awskms.DescribeKeyInput{
			KeyId: aws.String("alias/integration-test"),
		})
		assert.Equal(t, "NotFoundException", apiErrorCode(err))
	})
}

func TestKMSKeyStateTransitions(t *testing.T) {
	clients := newTestClients(t)
	ctx, cancel := context.WithTimeout(context.Background(), kmsTestTimeout)
	defer cancel()

	keyID := kmsCreateKey(ctx, t, clients, "state transition test key")

	// Subtests below are sequential: DisableKey must precede EnableKey.

	t.Run("Encrypt fails with DisabledException after the key is disabled", func(t *testing.T) {
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

	t.Run("Encrypt succeeds again after the key is re-enabled", func(t *testing.T) {
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
}

func TestKMSKeyRotation(t *testing.T) {
	clients := newTestClients(t)
	ctx, cancel := context.WithTimeout(context.Background(), kmsTestTimeout)
	defer cancel()

	keyID := kmsCreateKey(ctx, t, clients, "rotation test key")

	// Subtests below are sequential: EnableKeyRotation must precede DisableKeyRotation.

	t.Run("enables rotation with the specified period and status reflects it", func(t *testing.T) {
		_, err := clients.kms.EnableKeyRotation(ctx, &awskms.EnableKeyRotationInput{
			KeyId:                aws.String(keyID),
			RotationPeriodInDays: aws.Int32(90),
		})
		require.NoError(t, err)

		out, err := clients.kms.GetKeyRotationStatus(ctx, &awskms.GetKeyRotationStatusInput{
			KeyId: aws.String(keyID),
		})
		require.NoError(t, err)
		assert.True(t, out.KeyRotationEnabled)
		assert.Equal(t, int32(90), aws.ToInt32(out.RotationPeriodInDays))
	})

	t.Run("disabling rotation marks the status as not enabled", func(t *testing.T) {
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
}

func TestKMSKeyDeletion(t *testing.T) {
	clients := newTestClients(t)
	ctx, cancel := context.WithTimeout(context.Background(), kmsTestTimeout)
	defer cancel()

	keyID := kmsCreateKey(ctx, t, clients, "deletion test key")

	// Subtests below are sequential: ScheduleKeyDeletion must precede CancelKeyDeletion.

	t.Run(
		"Encrypt fails with KMSInvalidStateException while key is pending deletion",
		func(t *testing.T) {
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
		},
	)

	t.Run("key returns to Disabled state after deletion is cancelled", func(t *testing.T) {
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

func TestKMSTagResource(t *testing.T) {
	clients := newTestClients(t)
	ctx, cancel := context.WithTimeout(context.Background(), kmsTestTimeout)
	defer cancel()

	keyID := kmsCreateKey(ctx, t, clients, "tag resource test key")

	t.Run("adds tags and reads them back", func(t *testing.T) {
		_, err := clients.kms.TagResource(ctx, &awskms.TagResourceInput{
			KeyId: aws.String(keyID),
			Tags: []types.Tag{
				{TagKey: aws.String("Env"), TagValue: aws.String("test")},
				{TagKey: aws.String("Team"), TagValue: aws.String("platform")},
			},
		})
		require.NoError(t, err)

		out, err := clients.kms.ListResourceTags(ctx, &awskms.ListResourceTagsInput{
			KeyId: aws.String(keyID),
		})
		require.NoError(t, err)
		require.Len(t, out.Tags, 2)
		assert.False(t, out.Truncated)
	})

	t.Run("overwrites existing tag value", func(t *testing.T) {
		_, err := clients.kms.TagResource(ctx, &awskms.TagResourceInput{
			KeyId: aws.String(keyID),
			Tags:  []types.Tag{{TagKey: aws.String("Env"), TagValue: aws.String("prod")}},
		})
		require.NoError(t, err)

		out, err := clients.kms.ListResourceTags(ctx, &awskms.ListResourceTagsInput{
			KeyId: aws.String(keyID),
		})
		require.NoError(t, err)
		var found string
		for _, tag := range out.Tags {
			if aws.ToString(tag.TagKey) == "Env" {
				found = aws.ToString(tag.TagValue)
			}
		}
		assert.Equal(t, "prod", found)
	})

	t.Run("returns NotFoundException for unknown key", func(t *testing.T) {
		_, err := clients.kms.TagResource(ctx, &awskms.TagResourceInput{
			KeyId: aws.String("00000000-0000-0000-0000-000000000000"),
			Tags:  []types.Tag{{TagKey: aws.String("k"), TagValue: aws.String("v")}},
		})
		assert.Equal(t, "NotFoundException", apiErrorCode(err))
	})
}

func TestKMSUntagResource(t *testing.T) {
	clients := newTestClients(t)
	ctx, cancel := context.WithTimeout(context.Background(), kmsTestTimeout)
	defer cancel()

	keyID := kmsCreateKey(ctx, t, clients, "untag resource test key")

	_, err := clients.kms.TagResource(ctx, &awskms.TagResourceInput{
		KeyId: aws.String(keyID),
		Tags: []types.Tag{
			{TagKey: aws.String("A"), TagValue: aws.String("1")},
			{TagKey: aws.String("B"), TagValue: aws.String("2")},
		},
	})
	require.NoError(t, err)

	t.Run("removes specified tag", func(t *testing.T) {
		_, err := clients.kms.UntagResource(ctx, &awskms.UntagResourceInput{
			KeyId:   aws.String(keyID),
			TagKeys: []string{"A"},
		})
		require.NoError(t, err)

		out, err := clients.kms.ListResourceTags(ctx, &awskms.ListResourceTagsInput{
			KeyId: aws.String(keyID),
		})
		require.NoError(t, err)
		require.Len(t, out.Tags, 1)
		assert.Equal(t, "B", aws.ToString(out.Tags[0].TagKey))
	})

	t.Run("silently ignores non-existent tag key", func(t *testing.T) {
		_, err := clients.kms.UntagResource(ctx, &awskms.UntagResourceInput{
			KeyId:   aws.String(keyID),
			TagKeys: []string{"nonexistent"},
		})
		require.NoError(t, err)
	})

	t.Run("returns NotFoundException for unknown key", func(t *testing.T) {
		_, err := clients.kms.UntagResource(ctx, &awskms.UntagResourceInput{
			KeyId:   aws.String("00000000-0000-0000-0000-000000000000"),
			TagKeys: []string{"k"},
		})
		assert.Equal(t, "NotFoundException", apiErrorCode(err))
	})
}

func TestKMSListResourceTags(t *testing.T) {
	clients := newTestClients(t)
	ctx, cancel := context.WithTimeout(context.Background(), kmsTestTimeout)
	defer cancel()

	keyID := kmsCreateKey(ctx, t, clients, "list tags test key")

	t.Run("returns empty tags for a new key", func(t *testing.T) {
		out, err := clients.kms.ListResourceTags(ctx, &awskms.ListResourceTagsInput{
			KeyId: aws.String(keyID),
		})
		require.NoError(t, err)
		assert.Empty(t, out.Tags)
		assert.False(t, out.Truncated)
	})

	t.Run("returns NotFoundException for an unknown key ID", func(t *testing.T) {
		_, err := clients.kms.ListResourceTags(ctx, &awskms.ListResourceTagsInput{
			KeyId: aws.String("00000000-0000-0000-0000-000000000000"),
		})
		assert.Equal(t, "NotFoundException", apiErrorCode(err))
	})

	t.Run(
		"returns InvalidMarkerException when Marker refers to unknown tag key",
		func(t *testing.T) {
			_, err := clients.kms.ListResourceTags(ctx, &awskms.ListResourceTagsInput{
				KeyId:  aws.String(keyID),
				Marker: aws.String("invalid-marker"),
			})
			assert.Equal(t, "InvalidMarkerException", apiErrorCode(err))
		},
	)
}
