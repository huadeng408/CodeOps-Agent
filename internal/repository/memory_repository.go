// Package repository contains data-access code.
package repository

import (
	"code-agent/internal/model"

	"gorm.io/gorm"
)

// MemoryRepository defines persistence operations for memory data.
type MemoryRepository interface {
	GetWorkingSnapshot(userID uint, conversationID string) (*model.WorkingMemorySnapshot, error)
	UpsertWorkingSnapshot(snapshot *model.WorkingMemorySnapshot) error
	ListProfileSlots(userID uint, limit int) ([]*model.UserProfileSlot, error)
	GetProfileSlot(userID uint, slotKey string) (*model.UserProfileSlot, error)
	UpsertProfileSlot(slot *model.UserProfileSlot) error
	CreateLongTermMemory(memory *model.LongTermMemory) error
}

// memoryRepository implements persistence operations for memory data.
type memoryRepository struct {
	db *gorm.DB
}

// NewMemoryRepository creates a memory repository.
func NewMemoryRepository(db *gorm.DB) MemoryRepository {
	return &memoryRepository{db: db}
}

// GetWorkingSnapshot returns working snapshot.
func (r *memoryRepository) GetWorkingSnapshot(userID uint, conversationID string) (*model.WorkingMemorySnapshot, error) {
	var snapshot model.WorkingMemorySnapshot
	err := r.db.Where("user_id = ? AND conversation_id = ?", userID, conversationID).First(&snapshot).Error
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// UpsertWorkingSnapshot handles upsert working snapshot.
func (r *memoryRepository) UpsertWorkingSnapshot(snapshot *model.WorkingMemorySnapshot) error {
	var existing model.WorkingMemorySnapshot
	err := r.db.Where("user_id = ? AND conversation_id = ?", snapshot.UserID, snapshot.ConversationID).First(&existing).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return r.db.Create(snapshot).Error
		}
		return err
	}

	existing.Summary = snapshot.Summary
	existing.FactsJSON = snapshot.FactsJSON
	existing.EntitiesJSON = snapshot.EntitiesJSON
	existing.MessageCount = snapshot.MessageCount
	return r.db.Save(&existing).Error
}

// ListProfileSlots lists profile slots.
func (r *memoryRepository) ListProfileSlots(userID uint, limit int) ([]*model.UserProfileSlot, error) {
	if limit <= 0 {
		limit = 20
	}
	var slots []*model.UserProfileSlot
	err := r.db.Where("user_id = ?", userID).Order("updated_at desc").Limit(limit).Find(&slots).Error
	return slots, err
}

// GetProfileSlot returns profile slot.
func (r *memoryRepository) GetProfileSlot(userID uint, slotKey string) (*model.UserProfileSlot, error) {
	var slot model.UserProfileSlot
	err := r.db.Where("user_id = ? AND slot_key = ?", userID, slotKey).First(&slot).Error
	if err != nil {
		return nil, err
	}
	return &slot, nil
}

// UpsertProfileSlot handles upsert profile slot.
func (r *memoryRepository) UpsertProfileSlot(slot *model.UserProfileSlot) error {
	var existing model.UserProfileSlot
	err := r.db.Where("user_id = ? AND slot_key = ?", slot.UserID, slot.SlotKey).First(&existing).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return r.db.Create(slot).Error
		}
		return err
	}

	existing.SlotValue = slot.SlotValue
	existing.Confidence = slot.Confidence
	existing.Source = slot.Source
	return r.db.Save(&existing).Error
}

// CreateLongTermMemory creates long term memory.
func (r *memoryRepository) CreateLongTermMemory(memory *model.LongTermMemory) error {
	return r.db.Create(memory).Error
}
