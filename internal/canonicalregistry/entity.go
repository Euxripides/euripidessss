package canonicalregistry

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func (r *Repository) UpsertEntity(ctx context.Context, entity Entity) error {
	entity.EntityID = strings.ToLower(strings.TrimSpace(entity.EntityID))
	if !uuidRE.MatchString(entity.EntityID) {
		return fmt.Errorf("%w: invalid entity_id", ErrInvalidInput)
	}
	var err error
	if entity.Name, err = requiredText("entity_name", entity.Name, 512); err != nil {
		return err
	}
	if entity.Type, err = requiredText("entity_type", strings.ToUpper(entity.Type), 128); err != nil {
		return err
	}
	if entity.Website, err = optionalURL("website", entity.Website); err != nil {
		return err
	}
	if entity.RiskLevel, err = requiredText("risk_level", strings.ToUpper(entity.RiskLevel), 64); err != nil {
		return err
	}
	if entity.Source, err = requiredText("source", entity.Source, 128); err != nil {
		return err
	}
	if entity.Confidence, err = normalizeConfidence(entity.Confidence); err != nil {
		return err
	}
	if entity.UpdatedAt.IsZero() {
		entity.UpdatedAt = time.Now().UTC()
	} else if entity.UpdatedAt, err = requireTime("updated_at", entity.UpdatedAt); err != nil {
		return err
	}
	return r.insert(ctx, "onchain.entity_registry", []string{"entity_id", "entity_name", "entity_type", "website", "risk_level", "source", "confidence", "is_verified", "updated_at"},
		[]string{entity.EntityID, entity.Name, entity.Type, entity.Website, entity.RiskLevel, entity.Source, entity.Confidence, boolCSV(entity.Verified), formatTime(entity.UpdatedAt)})
}

func (r *Repository) GetEntity(ctx context.Context, entityID string) (Entity, error) {
	entityID = strings.ToLower(strings.TrimSpace(entityID))
	if !uuidRE.MatchString(entityID) {
		return Entity{}, fmt.Errorf("%w: invalid entity_id", ErrInvalidInput)
	}
	rows, err := r.query(ctx, fmt.Sprintf(`SELECT entity_id, entity_name, entity_type, website, risk_level, source, confidence, is_verified, updated_at
FROM onchain.entity_registry FINAL WHERE entity_id = '%s' LIMIT 1`, entityID))
	if err != nil {
		return Entity{}, err
	}
	if len(rows) == 0 {
		return Entity{}, ErrNotFound
	}
	entity, err := decodeEntity(rows[0])
	if err != nil || strings.ToLower(entity.EntityID) != entityID {
		return Entity{}, fmt.Errorf("%w: malformed entity row", ErrQueryFailed)
	}
	entity.EntityID = entityID
	return entity, nil
}

func (r *Repository) UpsertAddressLabel(ctx context.Context, label AddressLabel) error {
	address, err := normalizeAddress(label.ChainID, label.Address)
	if err != nil {
		return err
	}
	label.Address = address
	if label.LabelName, err = requiredText("label_name", label.LabelName, 512); err != nil {
		return err
	}
	label.LabelType = strings.ToUpper(strings.TrimSpace(label.LabelType))
	if !labelTypes[label.LabelType] {
		return fmt.Errorf("%w: invalid label_type", ErrInvalidInput)
	}
	if label.EntityID != nil {
		normalized := strings.ToLower(strings.TrimSpace(*label.EntityID))
		if !uuidRE.MatchString(normalized) {
			return fmt.Errorf("%w: invalid entity_id", ErrInvalidInput)
		}
		label.EntityID = &normalized
	}
	if label.EntityRole != "" {
		if label.EntityRole, err = requiredText("entity_role", label.EntityRole, 128); err != nil {
			return err
		}
	}
	if label.Source, err = requiredText("source", label.Source, 128); err != nil {
		return err
	}
	if label.Confidence, err = normalizeConfidence(label.Confidence); err != nil {
		return err
	}
	if len(label.Evidence) > 64<<10 || strings.ContainsRune(label.Evidence, 0) {
		return fmt.Errorf("%w: invalid evidence", ErrInvalidInput)
	}
	if label.FirstSeen, err = requireTime("first_seen", label.FirstSeen); err != nil {
		return err
	}
	if label.LastVerified, err = requireTime("last_verified", label.LastVerified); err != nil {
		return err
	}
	if label.LastVerified.Before(label.FirstSeen) {
		return fmt.Errorf("%w: last_verified before first_seen", ErrInvalidInput)
	}
	if label.UpdatedAt.IsZero() {
		label.UpdatedAt = time.Now().UTC()
	} else if label.UpdatedAt, err = requireTime("updated_at", label.UpdatedAt); err != nil {
		return err
	}
	return r.insert(ctx, "onchain.address_labels", []string{"chain_id", "address", "label_name", "label_type", "entity_id", "entity_role", "source", "confidence", "evidence", "first_seen", "last_verified", "updated_at"},
		[]string{strconv.FormatUint(uint64(label.ChainID), 10), label.Address, label.LabelName, label.LabelType, nullableCSV(label.EntityID), label.EntityRole, label.Source, label.Confidence, label.Evidence, formatTime(label.FirstSeen), formatTime(label.LastVerified), formatTime(label.UpdatedAt)})
}

func (r *Repository) ListAddressLabels(ctx context.Context, chainID uint32, address string, asOf *time.Time) ([]AddressLabel, error) {
	address, err := normalizeAddress(chainID, address)
	if err != nil {
		return nil, err
	}
	condition := ""
	if asOf != nil {
		if _, err = requireTime("as_of", *asOf); err != nil {
			return nil, err
		}
		condition = fmt.Sprintf(" AND first_seen <= parseDateTime64BestEffort('%s', 3, 'UTC') AND last_verified <= parseDateTime64BestEffort('%s', 3, 'UTC')", asOf.UTC().Format(time.RFC3339Nano), asOf.UTC().Format(time.RFC3339Nano))
	}
	rows, err := r.query(ctx, fmt.Sprintf(`SELECT chain_id, address, label_name, label_type, toString(entity_id) AS entity_id, entity_role, source, confidence, evidence, first_seen, last_verified, updated_at
FROM onchain.address_labels FINAL WHERE chain_id = %d AND address = '%s'%s ORDER BY confidence DESC, label_type ASC, label_name ASC`, chainID, address, condition))
	if err != nil {
		return nil, err
	}
	result := make([]AddressLabel, 0, len(rows))
	for _, row := range rows {
		item, decodeErr := decodeLabel(row)
		if decodeErr != nil || item.ChainID != chainID || strings.ToLower(item.Address) != address {
			return nil, fmt.Errorf("%w: malformed label row", ErrQueryFailed)
		}
		item.Address = address
		result = append(result, item)
	}
	return result, nil
}

func decodeEntity(row map[string]any) (Entity, error) {
	var out Entity
	var err error
	out.EntityID, err = stringValue(row["entity_id"])
	if err != nil {
		return out, err
	}
	out.Name, _ = stringValue(row["entity_name"])
	out.Type, _ = stringValue(row["entity_type"])
	out.Website, _ = stringValue(row["website"])
	out.RiskLevel, _ = stringValue(row["risk_level"])
	out.Source, _ = stringValue(row["source"])
	out.Confidence, _ = stringValue(row["confidence"])
	out.Verified, err = boolValue(row["is_verified"])
	if err != nil {
		return out, err
	}
	out.UpdatedAt, err = timeValue(row["updated_at"])
	return out, err
}

func decodeLabel(row map[string]any) (AddressLabel, error) {
	var out AddressLabel
	chain, err := uintValue(row["chain_id"], 32)
	if err != nil {
		return out, err
	}
	out.ChainID = uint32(chain)
	out.Address, err = stringValue(row["address"])
	if err != nil {
		return out, err
	}
	out.LabelName, _ = stringValue(row["label_name"])
	out.LabelType, _ = stringValue(row["label_type"])
	entityID, _ := stringValue(row["entity_id"])
	if entityID != "" {
		out.EntityID = &entityID
	}
	out.EntityRole, _ = stringValue(row["entity_role"])
	out.Source, _ = stringValue(row["source"])
	out.Confidence, _ = stringValue(row["confidence"])
	out.Evidence, _ = stringValue(row["evidence"])
	out.FirstSeen, err = timeValue(row["first_seen"])
	if err != nil {
		return out, err
	}
	out.LastVerified, err = timeValue(row["last_verified"])
	if err != nil {
		return out, err
	}
	out.UpdatedAt, err = timeValue(row["updated_at"])
	return out, err
}
