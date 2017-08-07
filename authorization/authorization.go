package authorization

import (
	"context"
	"net/http"
	"time"

	"github.com/gorilla/sessions"
	"github.com/jinzhu/gorm"
	"github.com/pkg/errors"
	"github.com/qor/roles"
)

type Authorization struct {
	db    *gorm.DB
	store sessions.Store

	permissions    map[string]*Permission
	superUserGroup *Group
}

func New() *Authorization {
	return &Authorization{
		permissions: make(map[string]*Permission),
	}
}

func (auth *Authorization) Configure(DB *gorm.DB, store sessions.Store) error {
	auth.db = DB
	auth.store = store

	permissions := []*Permission{}
	if err := auth.db.Find(&permissions).Error; err != nil {
		return errors.Wrap(err, "could not load permissions")
	}

	for _, permission := range permissions {
		auth.permissions[permission.Code] = permission
	}

	auth.superUserGroup = &Group{
		Name: "Super users",
	}
	query := auth.db.Preload("Permissions").First(auth.superUserGroup, auth.superUserGroup)
	if query.RecordNotFound() {
		if err := auth.db.Create(auth.superUserGroup).Error; err != nil {
			return errors.Wrap(err, "could not create super user group")
		}
	} else if query.Error != nil {
		return errors.Wrap(query.Error, "could not load super user group")
	}

	return nil
}

func (auth *Authorization) Resources() []interface{} {
	return []interface{}{
		&UserID{},
		&User{},
		&Group{},
		&Permission{},
	}
}

func (auth *Authorization) AddPermission(code, name string) error {
	if _, ok := auth.permissions[code]; !ok {
		permission := &Permission{
			Code: code,
			Name: name,
		}
		if err := auth.db.Create(permission).Error; err != nil {
			errors.Wrap(err, "could not create permission")
		}
		auth.permissions[code] = permission
	}

	inSuperUserGroup := false
	for _, permission := range auth.superUserGroup.Permissions {
		if permission.Code == code {
			inSuperUserGroup = true
			break
		}
	}
	if !inSuperUserGroup {
		auth.superUserGroup.Permissions = append(auth.superUserGroup.Permissions, *auth.permissions[code])
		if err := auth.db.Save(auth.superUserGroup).Error; err != nil {
			return errors.Wrap(err, "could not add permission to super user group")
		}
	}

	// this is for qor admin permission handling
	roles.Register(code, func(r *http.Request, userInt interface{}) bool {
		user := userInt.(User)
		return user.HasPermissions(code)
	})

	return nil
}

func (auth *Authorization) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, err := auth.store.Get(r, "authorization")
		if err != nil {
			// TODO: render error
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if id, ok := session.Values["userid"]; ok {
			var user User
			if auth.db.Joins("JOIN user_ids on user_ids.user_id = users.id AND user_ids.id = ?", id).Preload("Permissions").Preload("Groups.Permissions").First(&user, " active = ?", true).RecordNotFound() {
				delete(session.Values, "userid")
				err = session.Save(r, w)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			} else {
				ctx := context.WithValue(r.Context(), "user", user)
				r = r.WithContext(ctx)
			}
		}

		next.ServeHTTP(w, r)
	})
}

func (auth *Authorization) Login(w http.ResponseWriter, r *http.Request, id, name, email, avatarURL string) error {
	var userID UserID

	tx := auth.db.Begin()
	query := tx.Preload("User").First(&userID, "id = ?", id)
	if query.RecordNotFound() {
		userID.ID = id
		userID.User.Name = name
		userID.User.Email = email
		userID.User.AvatarURL = avatarURL
		userID.User.Active = true
		if err := tx.Save(&userID).Error; err != nil {
			tx.Rollback()
			return errors.Wrap(err, "could not create user id")
		}
	} else if query.Error != nil {
		tx.Rollback()
		return errors.Wrap(query.Error, "could not load user")
	}

	if !userID.User.Active {
		tx.Rollback()
		return errors.Errorf("User %s is not active, please contact administrator.", userID.User.Name)
	}

	userID.User.Name = name
	userID.User.Email = email
	userID.User.AvatarURL = avatarURL
	userID.User.LastLogin = time.Now()
	if err := tx.Save(&userID.User).Error; err != nil {
		tx.Rollback()
		return errors.Wrap(query.Error, "could not save user")
	}

	if err := tx.Commit().Error; err != nil {
		return errors.Wrap(err, "transaction failed")
	}

	session, err := auth.store.Get(r, "authorization")
	if err != nil {
		return errors.Wrap(err, "could not get session store")
	}

	session.Values["userid"] = id

	err = session.Save(r, w)
	if err != nil {
		return errors.Wrap(err, "could not save session")
	}

	return nil
}
