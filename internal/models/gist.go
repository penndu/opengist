package models

import (
	"gorm.io/gorm"
	"opengist/internal/git"
	"os/exec"
	"time"
)

type Gist struct {
	ID              uint `gorm:"primaryKey"`
	Uuid            string
	Title           string
	Preview         string
	PreviewFilename string
	Description     string
	Private         bool
	UserID          uint
	User            User
	NbFiles         int
	NbLikes         int
	NbForks         int
	CreatedAt       int64
	UpdatedAt       int64

	Likes    []User `gorm:"many2many:likes;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	Forked   *Gist  `gorm:"foreignKey:ForkedID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL"`
	ForkedID uint
}

type File struct {
	Filename    string `validate:"excludes=\x2f,excludes=\x5c,max=50"`
	OldFilename string `validate:"excludes=\x2f,excludes=\x5c,max=50"`
	Content     string `validate:"required"`
	Truncated   bool
}

type Commit struct {
	Hash      string
	Author    string
	Timestamp string
	Changed   string
	Files     []File
}

func (gist *Gist) BeforeDelete(tx *gorm.DB) error {
	// Decrement fork counter if the gist was forked
	err := tx.Model(&Gist{}).
		Omit("updated_at").
		Where("id = ?", gist.ForkedID).
		UpdateColumn("nb_forks", gorm.Expr("nb_forks - 1")).Error
	return err
}

func GetGist(user string, gistUuid string) (*Gist, error) {
	gist := new(Gist)
	err := db.Preload("User").Preload("Forked.User").
		Where("gists.uuid = ? AND users.username like ?", gistUuid, user).
		Joins("join users on gists.user_id = users.id").
		First(&gist).Error

	return gist, err
}

func GetGistByID(gistId string) (*Gist, error) {
	gist := new(Gist)
	err := db.Preload("User").Preload("Forked.User").
		Where("gists.id = ?", gistId).
		First(&gist).Error

	return gist, err
}

func GetAllGistsForCurrentUser(currentUserId uint, offset int, sort string, order string) ([]*Gist, error) {
	var gists []*Gist
	err := db.Preload("User").Preload("Forked.User").
		Where("gists.private = 0 or gists.user_id = ?", currentUserId).
		Limit(11).
		Offset(offset * 10).
		Order(sort + "_at " + order).
		Find(&gists).Error

	return gists, err
}

func GetAllGists(offset int) ([]*Gist, error) {
	var gists []*Gist
	err := db.Preload("User").
		Limit(11).
		Offset(offset * 10).
		Order("id asc").
		Find(&gists).Error

	return gists, err
}

func GetAllGistsFromUser(fromUser string, currentUserId uint, offset int, sort string, order string) ([]*Gist, error) {
	var gists []*Gist
	err := db.Preload("User").Preload("Forked.User").
		Where("users.username = ? and ((gists.private = 0) or (gists.private = 1 and gists.user_id = ?))", fromUser, currentUserId).
		Joins("join users on gists.user_id = users.id").
		Limit(11).
		Offset(offset * 10).
		Order("gists." + sort + "_at " + order).
		Find(&gists).Error

	return gists, err
}

func (gist *Gist) Create() error {
	// avoids foreign key constraint error because the default value in the struct is 0
	return db.Omit("forked_id").Create(&gist).Error
}

func (gist *Gist) CreateForked() error {
	return db.Create(&gist).Error
}

func (gist *Gist) Update() error {
	return db.Omit("forked_id").Save(&gist).Error
}

func (gist *Gist) Delete() error {
	return db.Delete(&gist).Error
}

func (gist *Gist) SetLastActiveNow() error {
	return db.Model(&Gist{}).
		Where("id = ?", gist.ID).
		Update("updated_at", time.Now().Unix()).Error
}

func (gist *Gist) AppendUserLike(user *User) error {
	err := db.Model(&gist).Omit("updated_at").Update("nb_likes", gist.NbLikes+1).Error
	if err != nil {
		return err
	}

	return db.Model(&gist).Omit("updated_at").Association("Likes").Append(user)
}

func (gist *Gist) RemoveUserLike(user *User) error {
	err := db.Model(&gist).Omit("updated_at").Update("nb_likes", gist.NbLikes-1).Error
	if err != nil {
		return err
	}

	return db.Model(&gist).Omit("updated_at").Association("Likes").Delete(user)
}

func (gist *Gist) IncrementForkCount() error {
	return db.Model(&gist).Omit("updated_at").Update("nb_forks", gist.NbForks+1).Error
}

func (gist *Gist) GetForkParent(user *User) (*Gist, error) {
	fork := new(Gist)
	err := db.Preload("User").
		Where("forked_id = ? and user_id = ?", gist.ID, user.ID).
		First(&fork).Error
	return fork, err
}

func (gist *Gist) GetUsersLikes(offset int) ([]*User, error) {
	var users []*User
	err := db.Model(&gist).
		Where("gist_id = ?", gist.ID).
		Limit(31).
		Offset(offset * 30).
		Association("Likes").Find(&users)
	return users, err
}

func (gist *Gist) GetForks(currentUserId uint, offset int) ([]*Gist, error) {
	var gists []*Gist
	err := db.Model(&gist).Preload("User").
		Where("forked_id = ?", gist.ID).
		Where("(gists.private = 0) or (gists.private = 1 and gists.user_id = ?)", currentUserId).
		Limit(11).
		Offset(offset * 10).
		Order("updated_at desc").
		Find(&gists).Error

	return gists, err
}

func (gist *Gist) CanWrite(user *User) bool {
	return !(user == nil) && (gist.UserID == user.ID)
}

func (gist *Gist) InitRepository() error {
	return git.InitRepository(gist.User.Username, gist.Uuid)
}

func (gist *Gist) DeleteRepository() error {
	return git.DeleteRepository(gist.User.Username, gist.Uuid)
}

func (gist *Gist) Files(revision string) ([]*File, error) {
	var files []*File
	filesStr, err := git.GetFilesOfRepository(gist.User.Username, gist.Uuid, revision)
	if err != nil {
		// if the revision or the file do not exist

		if exiterr, ok := err.(*exec.ExitError); ok && exiterr.ExitCode() == 128 {
			return nil, nil
		}

		return nil, err
	}

	for _, fileStr := range filesStr {
		file, err := gist.File(revision, fileStr, true)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, err
}

func (gist *Gist) File(revision string, filename string, truncate bool) (*File, error) {
	content, truncated, err := git.GetFileContent(gist.User.Username, gist.Uuid, revision, filename, truncate)

	// if the revision or the file do not exist
	if exiterr, ok := err.(*exec.ExitError); ok && exiterr.ExitCode() == 128 {
		return nil, nil
	}

	return &File{
		Filename:  filename,
		Content:   content,
		Truncated: truncated,
	}, err
}

func (gist *Gist) Log(skip string) error {
	_, err := git.GetLog(gist.User.Username, gist.Uuid, skip)

	return err
}

func (gist *Gist) NbCommits() (string, error) {
	return git.GetNumberOfCommitsOfRepository(gist.User.Username, gist.Uuid)
}

func (gist *Gist) AddAndCommitFiles(files *[]File) error {
	if err := git.CloneTmp(gist.User.Username, gist.Uuid, gist.Uuid); err != nil {
		return err
	}

	for _, file := range *files {
		if err := git.SetFileContent(gist.Uuid, file.Filename, file.Content); err != nil {
			return err
		}
	}

	if err := git.AddAll(gist.Uuid); err != nil {
		return err
	}

	if err := git.Commit(gist.Uuid); err != nil {
		return err
	}

	return git.Push(gist.Uuid)
}

func (gist *Gist) ForkClone(username string, uuid string) error {
	return git.ForkClone(gist.User.Username, gist.Uuid, username, uuid)
}

func (gist *Gist) UpdateServerInfo() error {
	return git.UpdateServerInfo(gist.User.Username, gist.Uuid)
}

func (gist *Gist) RPC(service string) ([]byte, error) {
	return git.RPC(gist.User.Username, gist.Uuid, service)
}

// -- DTO -- //

type GistDTO struct {
	Title       string `validate:"max=50" form:"title"`
	Description string `validate:"max=150" form:"description"`
	Private     bool   `form:"private"`
	Files       []File `validate:"min=1,dive"`
}

func (dto *GistDTO) ToGist() *Gist {
	return &Gist{
		Title:       dto.Title,
		Description: dto.Description,
		Private:     dto.Private,
	}
}

func (dto *GistDTO) ToExistingGist(gist *Gist) *Gist {
	gist.Title = dto.Title
	gist.Description = dto.Description
	gist.Private = dto.Private
	return gist
}
