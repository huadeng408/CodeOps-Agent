// Package model contains persistent models and DTOs.
package model

import "time"

// WorkingMemorySnapshot captures the working memory snapshot.
type WorkingMemorySnapshot struct {
	ID             uint      `gorm:"primaryKey;autoIncrement"`
	UserID         uint      `gorm:"index:idx_working_memory_user_conv,priority:1;not null"`
	ConversationID string    `gorm:"type:varchar(64);index:idx_working_memory_user_conv,priority:2;not null"`
	Summary        string    `gorm:"type:text;not null"`
	FactsJSON      string    `gorm:"type:text"`
	EntitiesJSON   string    `gorm:"type:text"`
	MessageCount   int       `gorm:"not null;default:0"`
	CreatedAt      time.Time `gorm:"autoCreateTime"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime"`
}

// TableName handles table name.
func (WorkingMemorySnapshot) TableName() string {
	return "working_memory_snapshots"
}

// UserProfileSlot represents an user profile slot.
type UserProfileSlot struct {
	ID         uint      `gorm:"primaryKey;autoIncrement"`
	UserID     uint      `gorm:"index:uk_user_profile_slot,unique;not null"`
	SlotKey    string    `gorm:"type:varchar(64);index:uk_user_profile_slot,unique;not null"`
	SlotValue  string    `gorm:"type:varchar(255);not null"`
	Confidence float64   `gorm:"not null;default:0"`
	Source     string    `gorm:"type:varchar(128)"`
	CreatedAt  time.Time `gorm:"autoCreateTime"`
	UpdatedAt  time.Time `gorm:"autoUpdateTime"`
}

// TableName handles table name.
func (UserProfileSlot) TableName() string {
	return "user_profile_slots"
}

// LongTermMemory represents a long term memory.
type LongTermMemory struct {
	ID             uint      `gorm:"primaryKey;autoIncrement"`
	MemoryID       string    `gorm:"type:varchar(96);uniqueIndex;not null"`
	UserID         uint      `gorm:"index;not null"`
	ConversationID string    `gorm:"type:varchar(64);index;not null"`
	MemoryType     string    `gorm:"type:varchar(32);index;not null"`
	Content        string    `gorm:"type:text;not null"`
	Summary        string    `gorm:"type:text"`
	EntitiesJSON   string    `gorm:"type:text"`
	Importance     float64   `gorm:"not null;default:0"`
	CreatedAt      time.Time `gorm:"autoCreateTime;index"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime"`
}

// TableName handles table name.
func (LongTermMemory) TableName() string {
	return "long_term_memories"
}

// MemoryEsDocument represents a memory ES document.
type MemoryEsDocument struct {
	MemoryID       string    `json:"memory_id"`
	UserID         uint      `json:"user_id"`
	ConversationID string    `json:"conversation_id"`
	MemoryType     string    `json:"memory_type"`
	TextContent    string    `json:"text_content"`
	Vector         []float32 `json:"vector"`
	Importance     float64   `json:"importance"`
	CreatedAt      time.Time `json:"created_at"`
}
