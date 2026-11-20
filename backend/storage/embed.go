package storage

import (
	"strings"

	"gorm.io/gorm"
)

// EmbedSites 外部嵌入站点配置仓储。
type EmbedSites struct{ db *gorm.DB }

func NewEmbedSites(db *gorm.DB) *EmbedSites { return &EmbedSites{db: db} }

func (r *EmbedSites) Create(s *EmbedSite) error { return r.db.Create(s).Error }
func (r *EmbedSites) Update(s *EmbedSite) error { return r.db.Save(s).Error }
func (r *EmbedSites) Delete(id uint) error      { return r.db.Delete(&EmbedSite{}, id).Error }

func (r *EmbedSites) FindByID(id uint) (*EmbedSite, error) {
	var s EmbedSite
	if err := r.db.First(&s, id).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *EmbedSites) FindBySlug(slug string) (*EmbedSite, error) {
	var s EmbedSite
	if err := r.db.Where("slug = ?", strings.TrimSpace(slug)).First(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *EmbedSites) List() ([]EmbedSite, error) {
	var list []EmbedSite
	if err := r.db.Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}
