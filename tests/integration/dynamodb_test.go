package integration_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsdynamodb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDynamoDBIntegration(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()

	const tableName = "test-items"

	t.Run("CreateTable", func(t *testing.T) {
		_, err := clients.ddb.CreateTable(ctx, &awsdynamodb.CreateTableInput{
			TableName: aws.String(tableName),
			KeySchema: []dbtypes.KeySchemaElement{
				{AttributeName: aws.String("pk"), KeyType: dbtypes.KeyTypeHash},
				{AttributeName: aws.String("sk"), KeyType: dbtypes.KeyTypeRange},
			},
			AttributeDefinitions: []dbtypes.AttributeDefinition{
				{AttributeName: aws.String("pk"), AttributeType: dbtypes.ScalarAttributeTypeS},
				{AttributeName: aws.String("sk"), AttributeType: dbtypes.ScalarAttributeTypeS},
			},
			BillingMode: dbtypes.BillingModePayPerRequest,
		})
		require.NoError(t, err)
	})

	t.Run("PutItem", func(t *testing.T) {
		items := []map[string]dbtypes.AttributeValue{
			{
				"pk":   &dbtypes.AttributeValueMemberS{Value: "user#1"},
				"sk":   &dbtypes.AttributeValueMemberS{Value: "profile"},
				"name": &dbtypes.AttributeValueMemberS{Value: "Alice"},
			},
			{
				"pk":   &dbtypes.AttributeValueMemberS{Value: "user#1"},
				"sk":   &dbtypes.AttributeValueMemberS{Value: "settings"},
				"name": &dbtypes.AttributeValueMemberS{Value: "Alice Settings"},
			},
		}
		for _, item := range items {
			_, err := clients.ddb.PutItem(ctx, &awsdynamodb.PutItemInput{
				TableName: aws.String(tableName),
				Item:      item,
			})
			require.NoError(t, err)
		}
	})

	t.Run("GetItem", func(t *testing.T) {
		out, err := clients.ddb.GetItem(ctx, &awsdynamodb.GetItemInput{
			TableName: aws.String(tableName),
			Key: map[string]dbtypes.AttributeValue{
				"pk": &dbtypes.AttributeValueMemberS{Value: "user#1"},
				"sk": &dbtypes.AttributeValueMemberS{Value: "profile"},
			},
		})
		require.NoError(t, err)

		name, ok := out.Item["name"].(*dbtypes.AttributeValueMemberS)
		require.True(t, ok)
		assert.Equal(t, "Alice", name.Value)
	})

	t.Run("Query", func(t *testing.T) {
		out, err := clients.ddb.Query(ctx, &awsdynamodb.QueryInput{
			TableName:              aws.String(tableName),
			KeyConditionExpression: aws.String("pk = :pk"),
			ExpressionAttributeValues: map[string]dbtypes.AttributeValue{
				":pk": &dbtypes.AttributeValueMemberS{Value: "user#1"},
			},
		})
		require.NoError(t, err)
		assert.EqualValues(t, 2, out.Count)
	})

	t.Run("Scan", func(t *testing.T) {
		out, err := clients.ddb.Scan(ctx, &awsdynamodb.ScanInput{
			TableName: aws.String(tableName),
		})
		require.NoError(t, err)
		assert.EqualValues(t, 2, out.Count)
	})

	t.Run("DeleteItem", func(t *testing.T) {
		_, err := clients.ddb.DeleteItem(ctx, &awsdynamodb.DeleteItemInput{
			TableName: aws.String(tableName),
			Key: map[string]dbtypes.AttributeValue{
				"pk": &dbtypes.AttributeValueMemberS{Value: "user#1"},
				"sk": &dbtypes.AttributeValueMemberS{Value: "profile"},
			},
		})
		require.NoError(t, err)

		out, err := clients.ddb.GetItem(ctx, &awsdynamodb.GetItemInput{
			TableName: aws.String(tableName),
			Key: map[string]dbtypes.AttributeValue{
				"pk": &dbtypes.AttributeValueMemberS{Value: "user#1"},
				"sk": &dbtypes.AttributeValueMemberS{Value: "profile"},
			},
		})
		require.NoError(t, err)
		assert.Empty(t, out.Item)
	})
}
