package integration_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsdynamodb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	awsstreams "github.com/aws/aws-sdk-go-v2/service/dynamodbstreams"
	stypes "github.com/aws/aws-sdk-go-v2/service/dynamodbstreams/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDynamoDBStreamsIntegration verifies the full DynamoDB Streams read path via
// the AWS SDK: ListStreams → DescribeStream → GetShardIterator → GetRecords.
// Sub-tests run sequentially and share state.
func TestDynamoDBStreamsIntegration(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()

	const tableName = "streams-test"

	// --- setup: create stream-enabled table ---
	t.Run("CreateTable with streaming enabled", func(t *testing.T) {
		_, err := clients.ddb.CreateTable(ctx, &awsdynamodb.CreateTableInput{
			TableName: aws.String(tableName),
			KeySchema: []dbtypes.KeySchemaElement{
				{AttributeName: aws.String("pk"), KeyType: dbtypes.KeyTypeHash},
			},
			AttributeDefinitions: []dbtypes.AttributeDefinition{
				{AttributeName: aws.String("pk"), AttributeType: dbtypes.ScalarAttributeTypeS},
			},
			BillingMode: dbtypes.BillingModePayPerRequest,
			StreamSpecification: &dbtypes.StreamSpecification{
				StreamEnabled:  aws.Bool(true),
				StreamViewType: dbtypes.StreamViewTypeNewAndOldImages,
			},
		})
		require.NoError(t, err)
	})

	// --- generate stream records ---
	t.Run("PutItem emits INSERT record", func(t *testing.T) {
		_, err := clients.ddb.PutItem(ctx, &awsdynamodb.PutItemInput{
			TableName: aws.String(tableName),
			Item: map[string]dbtypes.AttributeValue{
				"pk": &dbtypes.AttributeValueMemberS{Value: "k1"},
				"v":  &dbtypes.AttributeValueMemberS{Value: "hello"},
			},
		})
		require.NoError(t, err)
	})

	t.Run("PutItem overwrites item emitting MODIFY record", func(t *testing.T) {
		_, err := clients.ddb.PutItem(ctx, &awsdynamodb.PutItemInput{
			TableName: aws.String(tableName),
			Item: map[string]dbtypes.AttributeValue{
				"pk": &dbtypes.AttributeValueMemberS{Value: "k1"},
				"v":  &dbtypes.AttributeValueMemberS{Value: "world"},
			},
		})
		require.NoError(t, err)
	})

	t.Run("DeleteItem emits REMOVE record", func(t *testing.T) {
		_, err := clients.ddb.DeleteItem(ctx, &awsdynamodb.DeleteItemInput{
			TableName: aws.String(tableName),
			Key: map[string]dbtypes.AttributeValue{
				"pk": &dbtypes.AttributeValueMemberS{Value: "k1"},
			},
		})
		require.NoError(t, err)
	})

	// --- read the stream ---
	var streamArn string

	t.Run("ListStreams returns the table stream", func(t *testing.T) {
		out, err := clients.streams.ListStreams(ctx, &awsstreams.ListStreamsInput{
			TableName: aws.String(tableName),
		})
		require.NoError(t, err)
		require.Len(t, out.Streams, 1)
		assert.Equal(t, tableName, aws.ToString(out.Streams[0].TableName))
		streamArn = aws.ToString(out.Streams[0].StreamArn)
	})

	t.Run(
		"ListStreams with non-existent table returns ResourceNotFoundException",
		func(t *testing.T) {
			_, err := clients.streams.ListStreams(ctx, &awsstreams.ListStreamsInput{
				TableName: aws.String("no-such-table"),
			})
			require.Error(t, err)
			assert.Equal(t, "ResourceNotFoundException", apiErrorCode(err))
		},
	)

	var shardID string

	t.Run("DescribeStream returns shard info", func(t *testing.T) {
		require.NotEmpty(t, streamArn, "streamArn must be set by ListStreams sub-test")
		out, err := clients.streams.DescribeStream(ctx, &awsstreams.DescribeStreamInput{
			StreamArn: aws.String(streamArn),
		})
		require.NoError(t, err)
		desc := out.StreamDescription
		assert.Equal(t, stypes.StreamStatusEnabled, desc.StreamStatus)
		assert.Equal(t, stypes.StreamViewTypeNewAndOldImages, desc.StreamViewType)
		require.Len(t, desc.Shards, 1)
		shardID = aws.ToString(desc.Shards[0].ShardId)
	})

	t.Run("DescribeStream Limit=0 returns ValidationException", func(t *testing.T) {
		require.NotEmpty(t, streamArn)
		_, err := clients.streams.DescribeStream(ctx, &awsstreams.DescribeStreamInput{
			StreamArn: aws.String(streamArn),
			Limit:     aws.Int32(0),
		})
		require.Error(t, err)
		assert.Equal(t, "ValidationException", apiErrorCode(err))
	})

	var iterator string

	t.Run("GetShardIterator TRIM_HORIZON", func(t *testing.T) {
		require.NotEmpty(t, streamArn)
		require.NotEmpty(t, shardID)
		out, err := clients.streams.GetShardIterator(ctx, &awsstreams.GetShardIteratorInput{
			StreamArn:         aws.String(streamArn),
			ShardId:           aws.String(shardID),
			ShardIteratorType: stypes.ShardIteratorTypeTrimHorizon,
		})
		require.NoError(t, err)
		require.NotEmpty(t, aws.ToString(out.ShardIterator))
		iterator = aws.ToString(out.ShardIterator)
	})

	t.Run("GetRecords returns INSERT MODIFY REMOVE in order", func(t *testing.T) {
		require.NotEmpty(t, iterator)
		out, err := clients.streams.GetRecords(ctx, &awsstreams.GetRecordsInput{
			ShardIterator: aws.String(iterator),
		})
		require.NoError(t, err)
		require.Len(t, out.Records, 3)

		assert.Equal(t, stypes.OperationTypeInsert, out.Records[0].EventName)
		assert.NotNil(t, out.Records[0].Dynamodb.NewImage)
		assert.Nil(t, out.Records[0].Dynamodb.OldImage)

		assert.Equal(t, stypes.OperationTypeModify, out.Records[1].EventName)
		assert.NotNil(t, out.Records[1].Dynamodb.NewImage)
		assert.NotNil(t, out.Records[1].Dynamodb.OldImage)

		assert.Equal(t, stypes.OperationTypeRemove, out.Records[2].EventName)
		assert.Nil(t, out.Records[2].Dynamodb.NewImage)
		assert.NotNil(t, out.Records[2].Dynamodb.OldImage)
	})

	t.Run("GetRecords from LATEST returns no records", func(t *testing.T) {
		require.NotEmpty(t, streamArn)
		require.NotEmpty(t, shardID)
		out, err := clients.streams.GetShardIterator(ctx, &awsstreams.GetShardIteratorInput{
			StreamArn:         aws.String(streamArn),
			ShardId:           aws.String(shardID),
			ShardIteratorType: stypes.ShardIteratorTypeLatest,
		})
		require.NoError(t, err)
		recs, err := clients.streams.GetRecords(ctx, &awsstreams.GetRecordsInput{
			ShardIterator: aws.String(aws.ToString(out.ShardIterator)),
		})
		require.NoError(t, err)
		assert.Empty(t, recs.Records)
	})
}
