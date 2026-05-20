package dynamodb

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// TransactGetInput is one Get request within TransactGetItems.
type TransactGetInput struct {
	TableName string
	Key       map[string]any
}

// TransactPut is the Put action within TransactWriteItems.
type TransactPut struct {
	TableName                      string
	Item                           map[string]any
	Cond                           *ConditionCheck
	ReturnValuesOnConditionFailure string // "ALL_OLD" or ""
}

// TransactUpdate is the Update action within TransactWriteItems.
type TransactUpdate struct {
	TableName                      string
	Key                            map[string]any
	Updates                        map[string]any // pre-parsed by parseUpdateExpression
	Cond                           *ConditionCheck
	ReturnValuesOnConditionFailure string // "ALL_OLD" or ""
}

// TransactDelete is the Delete action within TransactWriteItems.
type TransactDelete struct {
	TableName                      string
	Key                            map[string]any
	Cond                           *ConditionCheck
	ReturnValuesOnConditionFailure string // "ALL_OLD" or ""
}

// TransactConditionCheck is the ConditionCheck action within TransactWriteItems.
type TransactConditionCheck struct {
	TableName                      string
	Key                            map[string]any
	Cond                           *ConditionCheck
	ReturnValuesOnConditionFailure string // "ALL_OLD" or ""
}

// TransactWriteAction is one action within a TransactWriteItems call.
// Exactly one field must be non-nil.
type TransactWriteAction struct {
	Put            *TransactPut
	Update         *TransactUpdate
	Delete         *TransactDelete
	ConditionCheck *TransactConditionCheck
}

// TransactGetItems reads multiple items across tables under a single read lock.
// Missing items are represented as nil in the returned slice.
func (s *Storage) TransactGetItems(gets []TransactGetInput) ([]map[string]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	type itemRef struct {
		tableName string
		keyHash   string
	}
	seen := make(map[itemRef]bool, len(gets))
	results := make([]map[string]any, len(gets))
	for i, g := range gets {
		meta, err := s.readTableMeta(g.TableName)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, ErrTableNotFound
			}
			return nil, err
		}
		k, err := itemKey(g.Key, meta.KeySchema)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrValidationException, err)
		}
		ref := itemRef{tableName: g.TableName, keyHash: k}
		if seen[ref] {
			return nil, fmt.Errorf(
				"%w: Transaction request cannot include multiple operations on one item",
				ErrValidationException,
			)
		}
		seen[ref] = true
		item, err := readJSON[map[string]any](s, filepath.Join(g.TableName, k+".json"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		results[i] = item // nil when not found
	}
	return results, nil
}

// readExistingItemLocked reads an item by its raw key map.
// Returns nil (not an error) when the item does not exist.
// Must be called with mu held.
func (s *Storage) readExistingItemLocked(
	tableName string,
	keyMap map[string]any,
	meta TableMetadata,
) (map[string]any, error) {
	k, err := itemKey(keyMap, meta.KeySchema)
	if err != nil {
		return nil, err
	}
	item, err := readJSON[map[string]any](s, filepath.Join(tableName, k+".json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return item, nil
}

// evalCondition evaluates a ConditionExpression against current item state.
// current may be nil when the item does not exist.
func evalCondition(current map[string]any, cond *ConditionCheck) error {
	if cond == nil || cond.Expr == "" {
		return nil
	}
	if current == nil {
		current = map[string]any{}
	}
	ok, err := evalFilterExpr(cond.Expr, current, cond.Names, cond.Values)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrValidationException, err)
	}
	if !ok {
		return ErrConditionalCheckFailed
	}
	return nil
}

// TransactWriteItems executes all write actions atomically under a single write lock.
//
// Phase 0: reject requests with duplicate primary key targets (ValidationException).
// Phase 1: evaluate every ConditionExpression; collect failures.
// Phase 2: apply all writes only if every condition passed.
func (s *Storage) TransactWriteItems(actions []TransactWriteAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Phase 0: duplicate primary key detection
	if err := s.checkDuplicateKeysLocked(actions); err != nil {
		return err
	}

	// Phase 1: condition checks
	reasons := make([]CancellationReason, len(actions))
	for i := range reasons {
		reasons[i] = CancellationReason{Code: "None"}
	}
	hasFailed := false
	for i, action := range actions {
		current, condErr := s.checkTransactActionCondLocked(action)
		if errors.Is(condErr, ErrConditionalCheckFailed) {
			reason := CancellationReason{
				Code:    "ConditionalCheckFailed",
				Message: "The conditional request failed",
			}
			if transactActionReturnsOldOnFailure(action) && current != nil {
				reason.Item = current
			}
			reasons[i] = reason
			hasFailed = true
		} else if condErr != nil {
			return condErr
		}
	}
	if hasFailed {
		return &TransactionCanceledError{Reasons: reasons}
	}

	// Phase 2: apply writes
	for _, action := range actions {
		if err := s.applyTransactActionLocked(action); err != nil {
			return err
		}
	}
	return nil
}

// transactActionReturnsOldOnFailure reports whether an action requests the
// current item state to be included in CancellationReasons on condition failure.
func transactActionReturnsOldOnFailure(action TransactWriteAction) bool {
	switch {
	case action.Put != nil:
		return action.Put.ReturnValuesOnConditionFailure == "ALL_OLD"
	case action.Delete != nil:
		return action.Delete.ReturnValuesOnConditionFailure == "ALL_OLD"
	case action.Update != nil:
		return action.Update.ReturnValuesOnConditionFailure == "ALL_OLD"
	case action.ConditionCheck != nil:
		return action.ConditionCheck.ReturnValuesOnConditionFailure == "ALL_OLD"
	}
	return false
}

// checkDuplicateKeysLocked returns ValidationException if any two actions target
// the same item (same table + same primary key). Must be called with mu held.
func (s *Storage) checkDuplicateKeysLocked(actions []TransactWriteAction) error {
	type itemRef struct {
		tableName string
		keyHash   string
	}
	seen := make(map[itemRef]bool, len(actions))
	for _, action := range actions {
		var tableName string
		var keyMap map[string]any
		switch {
		case action.Put != nil:
			tableName = action.Put.TableName
			keyMap = action.Put.Item
		case action.Delete != nil:
			tableName = action.Delete.TableName
			keyMap = action.Delete.Key
		case action.Update != nil:
			tableName = action.Update.TableName
			keyMap = action.Update.Key
		case action.ConditionCheck != nil:
			tableName = action.ConditionCheck.TableName
			keyMap = action.ConditionCheck.Key
		default:
			continue
		}
		meta, err := s.readTableMeta(tableName)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return ErrTableNotFound
			}
			return err
		}
		k, err := itemKey(keyMap, meta.KeySchema)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrValidationException, err)
		}
		ref := itemRef{tableName: tableName, keyHash: k}
		if seen[ref] {
			return fmt.Errorf(
				"%w: Transaction request cannot include multiple operations on one item",
				ErrValidationException,
			)
		}
		seen[ref] = true
	}
	return nil
}

// checkTransactActionCondLocked evaluates the ConditionExpression for a single action.
// Returns the item's current state (before any write) and any condition error.
// Must be called with mu held.
func (s *Storage) checkTransactActionCondLocked(
	action TransactWriteAction,
) (map[string]any, error) {
	switch {
	case action.Put != nil:
		meta, err := s.readTableMeta(action.Put.TableName)
		if err != nil {
			if errors.Is(
				err,
				os.ErrNotExist,
			) { // unreachable: Phase 0 verified table exists under write lock
				return nil, ErrTableNotFound
			}
			return nil, err
		}
		current, err := s.readExistingItemLocked(action.Put.TableName, action.Put.Item, meta)
		if err != nil {
			return nil, err
		}
		return current, evalCondition(current, action.Put.Cond)

	case action.Delete != nil:
		meta, err := s.readTableMeta(action.Delete.TableName)
		if err != nil {
			if errors.Is(
				err,
				os.ErrNotExist,
			) { // unreachable: Phase 0 verified table exists under write lock
				return nil, ErrTableNotFound
			}
			return nil, err
		}
		current, err := s.readExistingItemLocked(action.Delete.TableName, action.Delete.Key, meta)
		if err != nil {
			return nil, err
		}
		return current, evalCondition(current, action.Delete.Cond)

	case action.Update != nil:
		meta, err := s.readTableMeta(action.Update.TableName)
		if err != nil {
			if errors.Is(
				err,
				os.ErrNotExist,
			) { // unreachable: Phase 0 verified table exists under write lock
				return nil, ErrTableNotFound
			}
			return nil, err
		}
		current, err := s.readExistingItemLocked(action.Update.TableName, action.Update.Key, meta)
		if err != nil {
			return nil, err
		}
		return current, evalCondition(current, action.Update.Cond)

	case action.ConditionCheck != nil:
		meta, err := s.readTableMeta(action.ConditionCheck.TableName)
		if err != nil {
			if errors.Is(
				err,
				os.ErrNotExist,
			) { // unreachable: Phase 0 verified table exists under write lock
				return nil, ErrTableNotFound
			}
			return nil, err
		}
		current, err := s.readExistingItemLocked(
			action.ConditionCheck.TableName,
			action.ConditionCheck.Key,
			meta,
		)
		if err != nil {
			return nil, err
		}
		return current, evalCondition(current, action.ConditionCheck.Cond)
	}
	return nil, nil // unreachable: Phase 0 ensures exactly one action field is non-nil
}

// applyTransactActionLocked applies the write for a single action (no condition checks).
// Must be called with mu held.
func (s *Storage) applyTransactActionLocked(action TransactWriteAction) error {
	switch {
	case action.Put != nil:
		meta, err := s.readTableMeta(action.Put.TableName)
		if err != nil {
			return err
		}
		k, err := itemKey(action.Put.Item, meta.KeySchema)
		if err != nil { // unreachable: Phase 0 validated same key
			return err
		}
		return s.writeJSON(filepath.Join(action.Put.TableName, k+".json"), action.Put.Item)

	case action.Delete != nil:
		meta, err := s.readTableMeta(action.Delete.TableName)
		if err != nil {
			return err
		}
		k, err := itemKey(action.Delete.Key, meta.KeySchema)
		if err != nil { // unreachable: Phase 0 validated same key
			return err
		}
		err = s.removeFile(filepath.Join(action.Delete.TableName, k+".json"))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err

	case action.Update != nil:
		meta, err := s.readTableMeta(action.Update.TableName)
		if err != nil {
			return err
		}
		k, err := itemKey(action.Update.Key, meta.KeySchema)
		if err != nil { // unreachable: Phase 0 validated same key
			return err
		}
		itemPath := filepath.Join(action.Update.TableName, k+".json")
		item, err := readJSON[map[string]any](s, itemPath)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			item = make(map[string]any, len(action.Update.Key))
			for kk, v := range action.Update.Key {
				item[kk] = v
			}
		}
		for attr, val := range action.Update.Updates {
			switch op := val.(type) {
			case nil:
				delete(item, attr)
			case addOp:
				result, err := applyAddOp(item[attr], op.val)
				if err != nil {
					return fmt.Errorf("%w: %v", ErrValidationException, err)
				}
				item[attr] = result
			case deleteOp:
				result, err := applyDeleteOp(item[attr], op.val)
				if err != nil {
					return fmt.Errorf("%w: %v", ErrValidationException, err)
				}
				if result == nil {
					delete(item, attr)
				} else {
					item[attr] = result
				}
			case ifNotExistsOp:
				item[attr] = op.resolve(item)
			case listAppendOp:
				result, err := applyListAppendOp(item, op)
				if err != nil {
					return fmt.Errorf("%w: %v", ErrValidationException, err)
				}
				item[attr] = result
			case nestedSetOp:
				var resolved any
				switch v := op.val.(type) {
				case ifNotExistsOp:
					resolved = v.resolve(item)
				case listAppendOp:
					var err error
					resolved, err = applyListAppendOp(item, v)
					if err != nil {
						return fmt.Errorf("%w: %v", ErrValidationException, err)
					}
				default:
					resolved = op.val
				}
				if err := applyNestedSet(item, op.segs, resolved); err != nil {
					return fmt.Errorf("%w: %v", ErrValidationException, err)
				}
			case nestedRemoveOp:
				if err := applyNestedRemove(item, op.segs); err != nil { // unreachable: applyNestedRemove always returns nil
					return fmt.Errorf("%w: %v", ErrValidationException, err)
				}
			default: // unreachable: parseUpdateExpression only produces the sentinel types above
				item[attr] = val
			}
		}
		return s.writeJSON(itemPath, item)
	}
	return nil // ConditionCheck: no write needed
}
