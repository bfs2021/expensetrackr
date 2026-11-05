package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/nutmos/expensetrackr/pkg/common_type"
)

// pathUID reads the :uid path parameter, normalises it with
// common_type.NormalizeUID and checks it is a canonical UUID (UID.Valid).
// Writes 400 and returns false if not.
func pathUID(c *gin.Context) (common_type.UID, bool) {
	uid := common_type.NormalizeUID(c.Param("uid"))
	if !uid.Valid() {
		c.JSON(http.StatusBadRequest, errorBody{Error: "uid must be a UUID (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx)"})
		return "", false
	}
	return uid, true
}
