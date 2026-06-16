package dynamodb

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"time"
)

var kinesisStreamARNRe = regexp.MustCompile(
	`^arn:(?:aws|aws-cn|aws-us-gov):kinesis:[a-z0-9-]+:\d{12}:stream/\S+$`,
)

func (ro *Router) handleDescribeContinuousBackups(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName string `json:"TableName"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
			"invalid request body",
		)
		return
	}
	if req.TableName == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
			"TableName is required",
		)
		return
	}
	meta, err := ro.storage.DescribeContinuousBackups(req.TableName)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeTableNotFoundException,
				"Table not found: "+req.TableName,
			)
			return
		}
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalServerError,
			"internal server error",
		)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ContinuousBackupsDescription": toContinuousBackupsDescription(meta.PITR),
	})
}

func (ro *Router) handleUpdateContinuousBackups(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName                        string `json:"TableName"`
		PointInTimeRecoverySpecification *struct {
			PointInTimeRecoveryEnabled bool `json:"PointInTimeRecoveryEnabled"`
		} `json:"PointInTimeRecoverySpecification"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
			"invalid request body",
		)
		return
	}
	if req.TableName == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
			"TableName is required",
		)
		return
	}
	if req.PointInTimeRecoverySpecification == nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
			"PointInTimeRecoverySpecification is required",
		)
		return
	}
	meta, err := ro.storage.UpdateContinuousBackups(
		req.TableName,
		req.PointInTimeRecoverySpecification.PointInTimeRecoveryEnabled,
	)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeTableNotFoundException,
				"Table not found: "+req.TableName,
			)
			return
		}
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalServerError,
			"internal server error",
		)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ContinuousBackupsDescription": toContinuousBackupsDescription(meta.PITR),
	})
}

func toContinuousBackupsDescription(pitr *PITRStatus) map[string]any {
	pitrDesc := map[string]any{
		"PointInTimeRecoveryStatus": "DISABLED",
	}
	if pitr != nil && pitr.Enabled {
		pitrDesc["PointInTimeRecoveryStatus"] = "ENABLED"
		if pitr.EnabledAt != nil {
			pitrDesc["EarliestRestorableDateTime"] = float64(
				pitr.EnabledAt.Add(5 * time.Minute).Unix(),
			)
			pitrDesc["LatestRestorableDateTime"] = float64(
				time.Now().UTC().Add(-5 * time.Minute).Unix(),
			)
		}
	}
	return map[string]any{
		"ContinuousBackupsStatus":        "ENABLED",
		"PointInTimeRecoveryDescription": pitrDesc,
	}
}

func (ro *Router) handleDescribeKinesisStreamingDestination(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName string `json:"TableName"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
			"invalid request body",
		)
		return
	}
	if req.TableName == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
			"TableName is required",
		)
		return
	}
	dests, err := ro.storage.DescribeKinesisStreamingDestination(req.TableName)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeResourceNotFoundException,
				"Requested resource not found: Table: "+req.TableName+" not found",
			)
			return
		}
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalServerError,
			"internal server error",
		)
		return
	}
	result := make([]map[string]any, 0, len(dests))
	for _, d := range dests {
		result = append(result, toKinesisDestinationMap(d))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"TableName":                     req.TableName,
		"KinesisDataStreamDestinations": result,
	})
}

func (ro *Router) handleEnableKinesisStreamingDestination(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName                           string `json:"TableName"`
		StreamArn                           string `json:"StreamArn"`
		EnableKinesisStreamingConfiguration *struct {
			ApproximateCreationDateTimePrecision string `json:"ApproximateCreationDateTimePrecision"`
		} `json:"EnableKinesisStreamingConfiguration"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
			"invalid request body",
		)
		return
	}
	if req.TableName == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
			"TableName is required",
		)
		return
	}
	if req.StreamArn == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
			"StreamArn is required",
		)
		return
	}
	if !kinesisStreamARNRe.MatchString(req.StreamArn) {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
			"Invalid StreamArn",
		)
		return
	}
	precision := "MILLISECOND"
	if req.EnableKinesisStreamingConfiguration != nil &&
		req.EnableKinesisStreamingConfiguration.ApproximateCreationDateTimePrecision != "" {
		p := req.EnableKinesisStreamingConfiguration.ApproximateCreationDateTimePrecision
		if p != "MILLISECOND" && p != "MICROSECOND" {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeValidationException,
				"ApproximateCreationDateTimePrecision must be MILLISECOND or MICROSECOND",
			)
			return
		}
		precision = p
	}
	dest, wasActive, err := ro.storage.EnableKinesisStreamingDestination(
		req.TableName,
		req.StreamArn,
		precision,
	)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeResourceNotFoundException,
				"Requested resource not found: Table: "+req.TableName+" not found",
			)
			return
		}
		if errors.Is(err, ErrKinesisLimitExceeded) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeLimitExceededException,
				"you can enable at most 2 kinesis destinations per table",
			)
			return
		}
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalServerError,
			"internal server error",
		)
		return
	}
	destStatus := "ENABLING"
	if wasActive {
		destStatus = "UPDATING"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"TableName":         req.TableName,
		"StreamArn":         dest.StreamARN,
		"DestinationStatus": destStatus,
		"EnableKinesisStreamingConfiguration": map[string]any{
			"ApproximateCreationDateTimePrecision": dest.Precision,
		},
	})
}

func (ro *Router) handleDisableKinesisStreamingDestination(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName string `json:"TableName"`
		StreamArn string `json:"StreamArn"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
			"invalid request body",
		)
		return
	}
	if req.TableName == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
			"TableName is required",
		)
		return
	}
	if req.StreamArn == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
			"StreamArn is required",
		)
		return
	}
	if !kinesisStreamARNRe.MatchString(req.StreamArn) {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
			"Invalid StreamArn",
		)
		return
	}
	dest, err := ro.storage.DisableKinesisStreamingDestination(req.TableName, req.StreamArn)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeResourceNotFoundException,
				"Requested resource not found: Table: "+req.TableName+" not found",
			)
			return
		}
		if errors.Is(err, ErrKinesisDestinationNotFound) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeResourceNotFoundException,
				"Kinesis destination not found: "+req.StreamArn,
			)
			return
		}
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalServerError,
			"internal server error",
		)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"TableName":         req.TableName,
		"StreamArn":         dest.StreamARN,
		"DestinationStatus": "DISABLING",
	})
}

func toKinesisDestinationMap(d KinesisDestination) map[string]any {
	return map[string]any{
		"StreamArn":                            d.StreamARN,
		"DestinationStatus":                    d.Status,
		"ApproximateCreationDateTimePrecision": d.Precision,
	}
}
