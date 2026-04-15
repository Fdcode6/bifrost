package tables

import "time"

// TableHealthDetectionTargetPreference stores the persisted active probing preference for
// a concrete adaptive routing target identified by provider + model + key_id.
type TableHealthDetectionTargetPreference struct {
	TargetKey        string    `gorm:"primaryKey;type:text" json:"target_key"`
	Provider         string    `gorm:"type:varchar(255);not null;index" json:"provider"`
	Model            string    `gorm:"type:varchar(255);not null;index" json:"model"`
	KeyID            *string   `gorm:"type:varchar(255)" json:"key_id,omitempty"`
	DetectionEnabled bool      `gorm:"not null;default:false" json:"detection_enabled"`
	CreatedAt        time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt        time.Time `gorm:"index;not null" json:"updated_at"`
}

func (TableHealthDetectionTargetPreference) TableName() string {
	return "health_detection_target_preferences"
}
