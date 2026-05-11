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

// TestDynamoDBIntegration runs sub-tests sequentially against shared state.
// Each sub-test depends on the state left by the previous one; order matters.
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

	t.Run("UpdateItem", func(t *testing.T) {
		_, err := clients.ddb.UpdateItem(ctx, &awsdynamodb.UpdateItemInput{
			TableName: aws.String(tableName),
			Key: map[string]dbtypes.AttributeValue{
				"pk": &dbtypes.AttributeValueMemberS{Value: "user#1"},
				"sk": &dbtypes.AttributeValueMemberS{Value: "profile"},
			},
			UpdateExpression: aws.String("SET #n = :name"),
			ExpressionAttributeNames: map[string]string{
				"#n": "name",
			},
			ExpressionAttributeValues: map[string]dbtypes.AttributeValue{
				":name": &dbtypes.AttributeValueMemberS{Value: "Alice Updated"},
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
		name, ok := out.Item["name"].(*dbtypes.AttributeValueMemberS)
		require.True(t, ok)
		assert.Equal(t, "Alice Updated", name.Value)
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

	t.Run("Scan with FilterExpression", func(t *testing.T) {
		out, err := clients.ddb.Scan(ctx, &awsdynamodb.ScanInput{
			TableName:        aws.String(tableName),
			FilterExpression: aws.String("#n = :name"),
			ExpressionAttributeNames: map[string]string{
				"#n": "name",
			},
			ExpressionAttributeValues: map[string]dbtypes.AttributeValue{
				":name": &dbtypes.AttributeValueMemberS{Value: "Alice Updated"},
			},
		})
		require.NoError(t, err)
		assert.EqualValues(t, 1, out.Count)
	})

	t.Run("PutItem with ConditionExpression succeeds for new item", func(t *testing.T) {
		_, err := clients.ddb.PutItem(ctx, &awsdynamodb.PutItemInput{
			TableName: aws.String(tableName),
			Item: map[string]dbtypes.AttributeValue{
				"pk":   &dbtypes.AttributeValueMemberS{Value: "user#99"},
				"sk":   &dbtypes.AttributeValueMemberS{Value: "profile"},
				"name": &dbtypes.AttributeValueMemberS{Value: "New User"},
			},
			ConditionExpression: aws.String("attribute_not_exists(pk)"),
		})
		require.NoError(t, err)
	})

	t.Run("PutItem with ConditionExpression fails for existing item", func(t *testing.T) {
		_, err := clients.ddb.PutItem(ctx, &awsdynamodb.PutItemInput{
			TableName: aws.String(tableName),
			Item: map[string]dbtypes.AttributeValue{
				"pk":   &dbtypes.AttributeValueMemberS{Value: "user#1"},
				"sk":   &dbtypes.AttributeValueMemberS{Value: "profile"},
				"name": &dbtypes.AttributeValueMemberS{Value: "Should Not Overwrite"},
			},
			ConditionExpression: aws.String("attribute_not_exists(pk)"),
		})
		var condErr *dbtypes.ConditionalCheckFailedException
		require.ErrorAs(t, err, &condErr)
	})

	t.Run("BatchWriteItem", func(t *testing.T) {
		_, err := clients.ddb.BatchWriteItem(ctx, &awsdynamodb.BatchWriteItemInput{
			RequestItems: map[string][]dbtypes.WriteRequest{
				tableName: {
					{PutRequest: &dbtypes.PutRequest{Item: map[string]dbtypes.AttributeValue{
						"pk":   &dbtypes.AttributeValueMemberS{Value: "user#2"},
						"sk":   &dbtypes.AttributeValueMemberS{Value: "profile"},
						"name": &dbtypes.AttributeValueMemberS{Value: "Bob"},
					}}},
					{PutRequest: &dbtypes.PutRequest{Item: map[string]dbtypes.AttributeValue{
						"pk":   &dbtypes.AttributeValueMemberS{Value: "user#3"},
						"sk":   &dbtypes.AttributeValueMemberS{Value: "profile"},
						"name": &dbtypes.AttributeValueMemberS{Value: "Charlie"},
					}}},
				},
			},
		})
		require.NoError(t, err)
	})

	t.Run("BatchGetItem", func(t *testing.T) {
		out, err := clients.ddb.BatchGetItem(ctx, &awsdynamodb.BatchGetItemInput{
			RequestItems: map[string]dbtypes.KeysAndAttributes{
				tableName: {
					Keys: []map[string]dbtypes.AttributeValue{
						{
							"pk": &dbtypes.AttributeValueMemberS{Value: "user#2"},
							"sk": &dbtypes.AttributeValueMemberS{Value: "profile"},
						},
						{
							"pk": &dbtypes.AttributeValueMemberS{Value: "user#3"},
							"sk": &dbtypes.AttributeValueMemberS{Value: "profile"},
						},
					},
				},
			},
		})
		require.NoError(t, err)
		assert.Len(t, out.Responses[tableName], 2)
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
