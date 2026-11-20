// Package lmfpd 提供纯 Go 零外部依赖的大模型数字指纹识别引擎。
// 算法与校准参考库来自 MIT 许可的 Ikaleio/lm-detector 项目（见 data/LICENSE）。
//
// 支持外挂目录（如 ./data/lmfpd/）优先加载，缺失或为空时自动回退至内置默认参考库。
package lmfpd

import (
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

const (
	ChallengeCount = 3
	SourceEmbedded = "embedded"
	SourceExternal = "external"

	fileDetectorBin = "shared_detector.bin"
	fileModelsJSON  = "models.json"
	fileSuiteJSON   = "enrollment-suite.json"
)

//go:embed data/shared_detector.bin
var embeddedDetectorBin []byte

//go:embed data/models.json
var embeddedModelsJSON []byte

//go:embed data/enrollment-suite.json
var embeddedSuiteJSON []byte

//go:embed data/LICENSE
var License string

// Challenge 是一道数字指纹随机整数采样题。
type Challenge struct {
	Instructions  string `json:"instructions,omitempty"`
	Prompt        string `json:"prompt"`
	ExpectedCount int    `json:"expected_count"`
}

type detectorVector struct {
	Offset int `json:"$f32"`
	Length int `json:"n"`
}

type detectorScaling struct {
	Mean  detectorVector `json:"mean"`
	Scale detectorVector `json:"scale"`
}

type detectorFeatureBank struct {
	Mean         detectorVector     `json:"feature_mean"`
	Scale        detectorVector     `json:"feature_scale"`
	Basis        []detectorVector   `json:"nuisance_basis"`
	Centroids    []detectorVector   `json:"centroids"`
	Environments [][]detectorVector `json:"environment_centroids"`
}

type detectorModel struct {
	ID            string `json:"id"`
	DisplayName   string `json:"display_name"`
	Family        string `json:"family"`
	FamilyName    string `json:"family_name"`
	ResponseCount int    `json:"response_count"`
}

type detectorData struct {
	Schema          string         `json:"schema"`
	BaseSHA256      string         `json:"base_sha256"`
	ReferenceSHA256 string         `json:"source_reference_sha256"`
	BankBuiltAt     string         `json:"bank_built_at"`
	ModelIDs        []string       `json:"model_ids"`
	ResponseCounts  detectorVector `json:"response_counts"`
	Ranker          struct {
		HeadParams []detectorScaling  `json:"head_params"`
		FullParams []detectorScaling  `json:"full_params"`
		LDAWeights []detectorVector   `json:"lda_weights"`
		LDABias    detectorVector     `json:"lda_bias"`
		References [][]detectorVector `json:"references"`
		Bank       struct {
			Hellinger detectorFeatureBank `json:"hellinger"`
			Ordered   detectorFeatureBank `json:"ordered_blocks"`
		} `json:"bank"`
		Blend struct {
			Frequency  float64 `json:"frequency"`
			Positional float64 `json:"positional"`
		} `json:"blend"`
		Positional struct {
			Params  []detectorScaling `json:"params"`
			Weights []detectorVector  `json:"weights"`
			Bias    detectorVector    `json:"bias"`
		} `json:"positional"`
	} `json:"ranker"`
	Calibration struct {
		Schema  string  `json:"schema"`
		Method  string  `json:"method"`
		Tau     float64 `json:"tau"`
		Binding struct {
			BaseSHA256      string   `json:"base_sha256"`
			ReferenceSHA256 string   `json:"reference_sha256"`
			ModelIDs        []string `json:"model_ids"`
		} `json:"binding"`
	} `json:"calibration"`

	floats         []float64
	models         []detectorModel
	referenceNorms [][]float64
}

// Engine 封装已解码且不可变的参考库与采样题库，支持并发安全调用。
type Engine struct {
	detector detectorData
	suite    []Challenge
	revision string
	source   string
}

func (e *Engine) Revision() string   { return e.revision }
func (e *Engine) Source() string     { return e.source }
func (e *Engine) BuiltAt() string    { return e.detector.BankBuiltAt }
func (e *Engine) ModelCount() int    { return len(e.detector.models) }

// Challenges 从题库随机抽取 3 道不同挑战题。
func (e *Engine) Challenges() [ChallengeCount]Challenge {
	var out [ChallengeCount]Challenge
	perm := rand.Perm(len(e.suite))
	for i := 0; i < ChallengeCount && i < len(perm); i++ {
		out[i] = e.suite[perm[i]]
	}
	return out
}

// LoadEmbedded 加载二进制内置的默认指纹参考库。
func LoadEmbedded() (*Engine, error) {
	return decodeEngine(embeddedDetectorBin, embeddedModelsJSON, embeddedSuiteJSON, SourceEmbedded)
}

// Load 优先尝试从 externalDir 加载外挂参考库；目录不存在或文件不全时自动回退内置库。
func Load(externalDir string) (*Engine, string, error) {
	dir := strings.TrimSpace(externalDir)
	if dir != "" {
		binPath := filepath.Join(dir, fileDetectorBin)
		modelsPath := filepath.Join(dir, fileModelsJSON)
		suitePath := filepath.Join(dir, fileSuiteJSON)
		if fileExists(binPath) && fileExists(modelsPath) && fileExists(suitePath) {
			binRaw, err := os.ReadFile(binPath)
			if err != nil {
				return nil, "", fmt.Errorf("read external %s: %w", fileDetectorBin, err)
			}
			modelsRaw, err := os.ReadFile(modelsPath)
			if err != nil {
				return nil, "", fmt.Errorf("read external %s: %w", fileModelsJSON, err)
			}
			suiteRaw, err := os.ReadFile(suitePath)
			if err != nil {
				return nil, "", fmt.Errorf("read external %s: %w", fileSuiteJSON, err)
			}
			eng, err := decodeEngine(binRaw, modelsRaw, suiteRaw, SourceExternal)
			if err != nil {
				return nil, "", fmt.Errorf("decode external lmfpd data: %w", err)
			}
			return eng, SourceExternal, nil
		}
	}
	eng, err := LoadEmbedded()
	if err != nil {
		return nil, "", err
	}
	return eng, SourceEmbedded, nil
}

// Manager 负责懒加载与按需热重载外挂参考库。
type Manager struct {
	mu      sync.RWMutex
	dataDir string
	engine  *Engine
}

func NewManager(dataDir string) *Manager {
	return &Manager{dataDir: strings.TrimSpace(dataDir)}
}

func (m *Manager) SetDataDir(dir string) {
	m.mu.Lock()
	if strings.TrimSpace(dir) != m.dataDir {
		m.dataDir = strings.TrimSpace(dir)
		m.engine = nil
	}
	m.mu.Unlock()
}

func (m *Manager) Engine() (*Engine, error) {
	m.mu.RLock()
	if m.engine != nil {
		eng := m.engine
		m.mu.RUnlock()
		return eng, nil
	}
	dir := m.dataDir
	m.mu.RUnlock()

	eng, _, err := Load(dir)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.engine == nil {
		m.engine = eng
	} else {
		eng = m.engine
	}
	m.mu.Unlock()
	return eng, nil
}

func (m *Manager) Reload() (*Engine, error) {
	m.mu.RLock()
	dir := m.dataDir
	m.mu.RUnlock()
	eng, _, err := Load(dir)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.engine = eng
	m.mu.Unlock()
	return eng, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func decodeEngine(binRaw, modelsRaw, suiteRaw []byte, source string) (*Engine, error) {
	d, err := decodeDetector(binRaw, modelsRaw)
	if err != nil {
		return nil, err
	}
	suite, err := decodeSuite(suiteRaw)
	if err != nil {
		return nil, err
	}
	if len(suite) < ChallengeCount {
		return nil, fmt.Errorf("lmfpd suite has %d challenges, need at least %d", len(suite), ChallengeCount)
	}
	sum := sha256.Sum256(binRaw)
	return &Engine{
		detector: d,
		suite:    suite,
		revision: fmt.Sprintf("sha256:%x", sum),
		source:   source,
	}, nil
}

func decodeSuite(raw []byte) ([]Challenge, error) {
	var entries []struct {
		System        string `json:"system"`
		UserPrefix    string `json:"user_prefix"`
		Prompt        string `json:"prompt"`
		ExpectedCount int    `json:"expected_count"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("decode enrollment suite: %w", err)
	}
	challenges := make([]Challenge, len(entries))
	for i, entry := range entries {
		prompt := entry.Prompt
		if entry.UserPrefix != "" {
			prompt = entry.UserPrefix + "\n\nFinal task:\n" + prompt
		}
		challenges[i] = Challenge{
			Instructions:  entry.System,
			Prompt:        prompt,
			ExpectedCount: entry.ExpectedCount,
		}
	}
	return challenges, nil
}

func decodeDetector(file, modelsJSON []byte) (detectorData, error) {
	var d detectorData
	if len(file) < 4 {
		return d, errors.New("lm-detector: missing detector header")
	}
	headerSize := uint64(binary.LittleEndian.Uint32(file))
	start := (4 + headerSize + 3) &^ 3
	if start > uint64(len(file)) || (uint64(len(file))-start)%4 != 0 {
		return d, errors.New("lm-detector: truncated detector binary")
	}
	if err := json.Unmarshal(file[4:4+headerSize], &d); err != nil {
		return d, fmt.Errorf("lm-detector: decode header json: %w", err)
	}

	planes := file[start:]
	count := len(planes) / 4
	d.floats = make([]float64, count)
	for i := range d.floats {
		bits := uint32(planes[i]) |
			uint32(planes[count+i])<<8 |
			uint32(planes[2*count+i])<<16 |
			uint32(planes[3*count+i])<<24
		d.floats[i] = float64(math.Float32frombits(bits))
	}

	var bank struct {
		BuiltAt string          `json:"built_at"`
		Models  []detectorModel `json:"models"`
	}
	if err := json.Unmarshal(modelsJSON, &bank); err != nil {
		return d, fmt.Errorf("lm-detector: decode models json: %w", err)
	}

	counts, err := d.vector(d.ResponseCounts)
	if err != nil {
		return d, err
	}
	if d.Schema != "shared-detector-v2" || bank.BuiltAt != d.BankBuiltAt ||
		len(bank.Models) != len(d.ModelIDs) || len(bank.Models) != len(counts) {
		return d, errors.New("lm-detector: reference bank does not match detector binary")
	}
	for i, m := range bank.Models {
		if m.ID != d.ModelIDs[i] || float64(m.ResponseCount) != counts[i] {
			return d, errors.New("lm-detector: model list mismatch with detector binary")
		}
	}

	c := &d.Calibration
	if c.Schema != "shared-confidence-v2" || c.Method != "ranking-temperature" || !(c.Tau >= 0.001 && c.Tau <= 1000) ||
		c.Binding.BaseSHA256 != d.BaseSHA256 || c.Binding.ReferenceSHA256 != d.ReferenceSHA256 || !slices.Equal(c.Binding.ModelIDs, d.ModelIDs) {
		return d, errors.New("lm-detector: calibration binding mismatch")
	}

	d.models = bank.Models
	d.referenceNorms = make([][]float64, 0, len(d.Ranker.References))
	for _, references := range d.Ranker.References {
		norms := make([]float64, len(references))
		for i, ref := range references {
			row, err := d.vector(ref)
			if err != nil {
				return d, err
			}
			norms[i] = dot(row, row)
		}
		d.referenceNorms = append(d.referenceNorms, norms)
	}
	return d, nil
}

func (d *detectorData) vector(ref detectorVector) ([]float64, error) {
	if ref.Offset < 0 || ref.Offset > len(d.floats) || ref.Length < 0 || ref.Length > len(d.floats)-ref.Offset {
		return nil, errors.New("lm-detector: float vector reference out of range")
	}
	end := ref.Offset + ref.Length
	return d.floats[ref.Offset:end:end], nil
}

func (d *detectorData) mustVector(ref detectorVector) []float64 {
	v, err := d.vector(ref)
	if err != nil {
		return nil
	}
	return v
}
