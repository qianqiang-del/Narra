package classroom

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"narra/pkg/objectstorage"
)

// AudioStore 是课堂音频的存放位置：保存一段合成音频，返回"可对外播放的地址"。
//
// 本地实现返回相对路径（<课堂ID>/<段落>.wav，前端补 /audio/ 前缀）；
// 对象存储实现返回完整 URL。两种形态前端都认（见音频播放组件）。
type AudioStore interface {
	// Save 保存一段音频；同名覆盖（断点续传重合成同一段是幂等的）。
	// 返回的地址直接写进 scene_segments.audio_path。
	Save(classroomID uint64, name string, data []byte) (string, error)
	// RemoveClassroom 删除整门课的音频；不存在时幂等成功。
	RemoveClassroom(classroomID uint64) error
}

// localAudioStore 是本地磁盘实现：<audioDir>/<课堂ID>/<段落>.wav。
type localAudioStore struct {
	audioDir string
}

// NewLocalAudioStore 创建本地音频存储。目录为空时返回 nil（调用方按"未接入"处理）。
func NewLocalAudioStore(audioDir string) AudioStore {
	if strings.TrimSpace(audioDir) == "" {
		return nil
	}
	return &localAudioStore{audioDir: audioDir}
}

// Save 实现 AudioStore：先写同目录临时文件再改名，中断时不留下半截 wav。
func (s *localAudioStore) Save(classroomID uint64, name string, data []byte) (string, error) {
	dir := filepath.Join(s.audioDir, strconv.FormatUint(classroomID, 10))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp := filepath.Join(dir, name+".tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		return "", err
	}
	// 与改造前完全一致的相对路径：静态路由 /audio 与旧数据用的是同一套拼接规则。
	return filepath.ToSlash(filepath.Join(strconv.FormatUint(classroomID, 10), name)), nil
}

// RemoveClassroom 实现 AudioStore。
func (s *localAudioStore) RemoveClassroom(classroomID uint64) error {
	return os.RemoveAll(filepath.Join(s.audioDir, strconv.FormatUint(classroomID, 10)))
}

// audioObjectKeyPrefix 是课堂音频在对象存储里的 key 空间。
const audioObjectKeyPrefix = "knowledge/audio"

// ossAudioStore 是对象存储实现：knowledge/audio/<课堂ID>/<段落>.wav，返回完整 URL。
type ossAudioStore struct {
	client objectstorage.Client
}

// NewOSSAudioStore 创建对象存储音频存储。
func NewOSSAudioStore(client objectstorage.Client) (AudioStore, error) {
	if client == nil {
		return nil, fmt.Errorf("对象存储客户端不能为空")
	}
	return &ossAudioStore{client: client}, nil
}

// Save 实现 AudioStore。同名覆盖：段落 ID 是文件名，重合成同一段落时原地替换。
func (s *ossAudioStore) Save(classroomID uint64, name string, data []byte) (string, error) {
	key := audioObjectKey(classroomID, name)
	if err := s.client.PutBytes(key, data); err != nil {
		return "", fmt.Errorf("上传课堂音频失败: %w", err)
	}
	return s.client.URL(key), nil
}

// RemoveClassroom 实现 AudioStore：按前缀清理整门课的音频。
func (s *ossAudioStore) RemoveClassroom(classroomID uint64) error {
	return s.client.DeletePrefix(audioObjectKeyPrefix + "/" + strconv.FormatUint(classroomID, 10))
}

// audioObjectKey 拼一段音频的 key。
func audioObjectKey(classroomID uint64, name string) string {
	return audioObjectKeyPrefix + "/" + strconv.FormatUint(classroomID, 10) + "/" + name
}
