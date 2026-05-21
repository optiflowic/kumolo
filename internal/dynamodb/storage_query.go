package dynamodb

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
)

// dynamoValueCmp compares two DynamoDB typed attribute values.
// Returns negative, zero, or positive like strings.Compare.
func dynamoValueCmp(a, b any) (int, error) {
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if !aok || !bok {
		return 0, fmt.Errorf("invalid DynamoDB typed value")
	}
	if as, ok := am["S"].(string); ok {
		bs, ok := bm["S"].(string)
		if !ok {
			return 0, fmt.Errorf("type mismatch: expected S")
		}
		return strings.Compare(as, bs), nil
	}
	if an, ok := am["N"].(string); ok {
		bn, ok := bm["N"].(string)
		if !ok {
			return 0, fmt.Errorf("type mismatch: expected N")
		}
		af, _ := strconv.ParseFloat(an, 64) // N values are always valid numerics per DynamoDB spec
		bf, _ := strconv.ParseFloat(bn, 64)
		switch {
		case af < bf:
			return -1, nil
		case af > bf:
			return 1, nil
		default:
			return 0, nil
		}
	}
	// Fallback: lexicographic JSON comparison (for B, BOOL, NULL, etc.)
	aj, _ := json.Marshal(a) // json.Marshal only fails for unmarshalable types (channels, funcs)
	bj, _ := json.Marshal(b) // json.Marshal only fails for unmarshalable types (channels, funcs)
	return strings.Compare(string(aj), string(bj)), nil
}

// matchesSortKey reports whether itemVal satisfies cond.
func matchesSortKey(itemVal any, cond SortKeyCondition) bool {
	switch cond.Operator {
	case OpEQ:
		a, _ := json.Marshal(
			itemVal,
		) // json.Marshal only fails for unmarshalable types (channels, funcs)
		b, _ := json.Marshal(
			cond.Value,
		) // json.Marshal only fails for unmarshalable types (channels, funcs)
		return string(a) == string(b)
	case OpLT:
		c, err := dynamoValueCmp(itemVal, cond.Value)
		return err == nil && c < 0
	case OpLTE:
		c, err := dynamoValueCmp(itemVal, cond.Value)
		return err == nil && c <= 0
	case OpGT:
		c, err := dynamoValueCmp(itemVal, cond.Value)
		return err == nil && c > 0
	case OpGTE:
		c, err := dynamoValueCmp(itemVal, cond.Value)
		return err == nil && c >= 0
	case OpBETWEEN:
		c1, err1 := dynamoValueCmp(itemVal, cond.Value)
		c2, err2 := dynamoValueCmp(itemVal, cond.Value2)
		return err1 == nil && err2 == nil && c1 >= 0 && c2 <= 0
	case OpBeginsWith:
		am, aok := itemVal.(map[string]any)
		bm, bok := cond.Value.(map[string]any)
		if !aok || !bok {
			return false
		}
		as, aok := am["S"].(string)
		bs, bok := bm["S"].(string)
		return aok && bok && strings.HasPrefix(as, bs)
	}
	return false
}

func (s *Storage) Scan(
	tableName string,
	opts ScanOptions,
) ([]map[string]any, map[string]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.tableExistsLocked(tableName) {
		return nil, nil, ErrTableNotFound
	}

	meta, err := s.readTableMeta(tableName)
	if err != nil {
		return nil, nil, err
	}

	all, err := s.readAllItemsLocked(tableName)
	if err != nil {
		return nil, nil, err
	}

	if meta.TTL != nil && meta.TTL.Enabled {
		filtered := all[:0]
		for _, item := range all {
			if !isTTLExpired(item, meta.TTL) {
				filtered = append(filtered, item)
			}
		}
		all = filtered
	}

	// Sort items by primary key for deterministic order regardless of filesystem
	// ReadDir order. Required for the k > eskKey comparison that resumes
	// scanning after a deleted item.
	sort.Slice(all, func(i, j int) bool {
		ki, _ := itemKey(all[i], meta.KeySchema)
		kj, _ := itemKey(all[j], meta.KeySchema)
		return ki < kj
	})

	// Segment partitioning must precede ESK so that ESK resumes within the
	// correct segment, not across the global item list.
	if opts.Segment != nil && opts.TotalSegments != nil {
		seg := *opts.Segment
		total := *opts.TotalSegments
		var segItems []map[string]any
		for i, item := range all {
			if i%total == seg {
				segItems = append(segItems, item)
			}
		}
		all = segItems
	}

	if len(opts.ExclusiveStartKey) > 0 {
		eskKey, err := itemKey(opts.ExclusiveStartKey, meta.KeySchema)
		if err != nil {
			return nil, nil, err
		}
		startIdx := len(all) // default: past end
		for i, item := range all {
			k, kErr := itemKey(item, meta.KeySchema)
			if kErr != nil {
				continue // untestable: kumolo-written items always include required key attributes
			}
			if k == eskKey {
				startIdx = i + 1
				break
			}
			// Items were sorted by hash above, so a hash that already exceeds
			// eskKey means the ESK item was deleted. Resume from this position.
			if k > eskKey {
				startIdx = i
				break
			}
		}
		all = all[startIdx:]
	}

	var lastEvaluatedKey map[string]any
	if opts.Limit != nil && len(all) > *opts.Limit {
		lastItem := all[*opts.Limit-1]
		lastEvaluatedKey = extractPrimaryKey(lastItem, meta.KeySchema)
		all = all[:*opts.Limit]
	}

	return all, lastEvaluatedKey, nil
}

// Query returns items in tableName matching the hash key equality and the optional
// sort key condition. Hash key comparison uses JSON encoding; sort key comparison
// is type-aware (S: lexicographic, N: numeric).
//
// opts.ScanIndexForward controls ascending (true) vs descending (false) sort order.
// opts.Limit caps the number of items evaluated before FilterExpression.
// opts.ExclusiveStartKey resumes from the item after the given primary key.
// The second return value is the LastEvaluatedKey (non-nil when more pages remain).
func (s *Storage) Query(
	tableName, hashKeyName string,
	hashKeyValue any,
	skCond *SortKeyCondition,
	opts QueryOptions,
) ([]map[string]any, map[string]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.tableExistsLocked(tableName) {
		return nil, nil, ErrTableNotFound
	}
	meta, err := s.readTableMeta(tableName)
	if err != nil {
		return nil, nil, err
	}
	var skName string
	lekSchema := meta.KeySchema
	var indexProjection map[string]any
	if opts.IndexName != "" {
		idxSchema, proj, err := findIndexDef(meta, opts.IndexName)
		if err != nil {
			return nil, nil, err
		}
		indexProjection = proj
		var idxHashKey string
		for _, k := range idxSchema {
			switch k.KeyType {
			case "HASH":
				idxHashKey = k.AttributeName
			case "RANGE":
				skName = k.AttributeName
			}
		}
		if hashKeyName != idxHashKey {
			return nil, nil, fmt.Errorf(
				"%w: query key condition attribute '%s' does not match the HASH key of index '%s' (expected '%s')",
				ErrValidationException,
				hashKeyName,
				opts.IndexName,
				idxHashKey,
			)
		}
		lekSchema = mergeKeySchemas(meta.KeySchema, idxSchema)
	} else {
		for _, k := range meta.KeySchema {
			if k.KeyType == "RANGE" {
				skName = k.AttributeName
				break
			}
		}
	}

	all, err := s.readAllItemsLocked(tableName)
	if err != nil {
		return nil, nil, err
	}
	wantJSON, _ := json.Marshal(hashKeyValue) // only fails for channels/funcs
	var matched []map[string]any
	for _, item := range all {
		if isTTLExpired(item, meta.TTL) {
			continue
		}
		val, ok := item[hashKeyName]
		if !ok {
			continue
		}
		gotJSON, _ := json.Marshal(val) // only fails for channels/funcs
		if string(gotJSON) != string(wantJSON) {
			continue
		}
		if skCond != nil {
			skVal, ok := item[skCond.Name]
			if !ok || !matchesSortKey(skVal, *skCond) {
				continue
			}
		}
		matched = append(matched, item)
	}

	if skName != "" {
		sort.SliceStable(matched, func(i, j int) bool {
			c, err := dynamoValueCmp(matched[i][skName], matched[j][skName])
			if err != nil {
				slog.Warn(
					"Query: sort key comparison failed; order undefined",
					"table",
					tableName,
					"err",
					err,
				)
				return false
			}
			if opts.ScanIndexForward {
				return c < 0
			}
			return c > 0
		})
	}

	if len(opts.ExclusiveStartKey) > 0 {
		if skName == "" {
			// Hash-only index: any ExclusiveStartKey means we are resuming past that item.
			matched = matched[:0]
		} else {
			eskSKVal, ok := opts.ExclusiveStartKey[skName]
			if !ok {
				matched = matched[:0]
			} else {
				// Use sort key value as a position cursor, not an exact item lookup.
				// This matches DynamoDB behavior: the cursor remains valid even when
				// the item at that key has been deleted.
				startIdx := len(matched)
				for i, item := range matched {
					c, err := dynamoValueCmp(item[skName], eskSKVal)
					if err != nil {
						continue
					}
					if opts.ScanIndexForward && c > 0 {
						startIdx = i
						break
					}
					if !opts.ScanIndexForward && c < 0 {
						startIdx = i
						break
					}
				}
				matched = matched[startIdx:]
			}
		}
	}

	var lastEvaluatedKey map[string]any
	if opts.Limit != nil && len(matched) > *opts.Limit {
		lastItem := matched[*opts.Limit-1]
		lastEvaluatedKey = extractPrimaryKey(lastItem, lekSchema)
		matched = matched[:*opts.Limit]
	}

	if opts.IndexName != "" {
		keyAttrNames := make([]string, len(lekSchema))
		for i, k := range lekSchema {
			keyAttrNames[i] = k.AttributeName
		}
		matched = applyIndexProjection(matched, indexProjection, keyAttrNames)
	}

	return matched, lastEvaluatedKey, nil
}

// findIndexDef returns the key schema and projection of the named GSI or LSI.
func findIndexDef(
	meta TableMetadata,
	indexName string,
) (keySchema []KeySchemaElement, projection map[string]any, err error) {
	for _, gsi := range meta.GlobalSecondaryIndexes {
		if gsi.IndexName == indexName {
			return gsi.KeySchema, gsi.Projection, nil
		}
	}
	for _, lsi := range meta.LocalSecondaryIndexes {
		if lsi.IndexName == indexName {
			return lsi.KeySchema, lsi.Projection, nil
		}
	}
	return nil, nil, fmt.Errorf(
		"%w: index %q does not exist on table",
		ErrValidationException,
		indexName,
	)
}

// mergeKeySchemas returns a deduped union of indexSchema then tableSchema, index keys first.
func mergeKeySchemas(tableSchema, indexSchema []KeySchemaElement) []KeySchemaElement {
	seen := make(map[string]bool, len(tableSchema)+len(indexSchema))
	merged := make([]KeySchemaElement, 0, len(tableSchema)+len(indexSchema))
	for _, k := range indexSchema {
		if !seen[k.AttributeName] {
			seen[k.AttributeName] = true
			merged = append(merged, k)
		}
	}
	for _, k := range tableSchema {
		if !seen[k.AttributeName] {
			seen[k.AttributeName] = true
			merged = append(merged, k)
		}
	}
	return merged
}

// applyIndexProjection filters item attributes per the index ProjectionType (ALL/KEYS_ONLY/INCLUDE).
func applyIndexProjection(
	items []map[string]any,
	projection map[string]any,
	keyAttrNames []string,
) []map[string]any {
	if projection == nil {
		return items
	}
	projType, _ := projection["ProjectionType"].(string)
	if projType == "" || projType == "ALL" {
		return items
	}

	keep := make(map[string]bool, len(keyAttrNames))
	for _, k := range keyAttrNames {
		keep[k] = true
	}
	if projType == "INCLUDE" {
		if nonKeyAttrs, ok := projection["NonKeyAttributes"].([]any); ok {
			for _, a := range nonKeyAttrs {
				if s, ok := a.(string); ok {
					keep[s] = true
				}
			}
		}
	}

	result := make([]map[string]any, len(items))
	for i, item := range items {
		projected := make(map[string]any, len(keep))
		for attr := range keep {
			if v, ok := item[attr]; ok {
				projected[attr] = v
			}
		}
		result[i] = projected
	}
	return result
}

func extractPrimaryKey(item map[string]any, keySchema []KeySchemaElement) map[string]any {
	key := make(map[string]any, len(keySchema))
	for _, k := range keySchema {
		key[k.AttributeName] = item[k.AttributeName]
	}
	return key
}
