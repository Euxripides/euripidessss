package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/etl/backend/internal/dbimport"
)

func registerDBImportRoutes(api *gin.RouterGroup) {
	api.GET("/db/connections", HandleDBListConnections)
	api.POST("/db/connections", HandleDBSaveConnection)
	api.POST("/db/connections/test", HandleDBTestUnsavedConnection)
	api.PUT("/db/connections/:id", HandleDBUpdateConnection)
	api.DELETE("/db/connections/:id", HandleDBDeleteConnection)
	api.POST("/db/connections/:id/test", HandleDBTestConnection)
	api.GET("/db/connections/:id/databases", HandleDBDatabases)
	api.GET("/db/connections/:id/schemas", HandleDBSchemas)
	api.GET("/db/connections/:id/tables", HandleDBTables)
	api.GET("/db/connections/:id/columns", HandleDBColumns)
	api.GET("/db/connections/:id/indexes", HandleDBIndexes)
	api.POST("/db/preview", HandleDBPreview)
	api.POST("/db/search", HandleDBSearch)
	api.POST("/db/query", HandleDBQuery)
	api.POST("/db/query/cancel", HandleDBQueryCancel)
	api.POST("/db/table/insert", HandleDBInsert)
	api.PUT("/db/table/update", HandleDBUpdate)
	api.DELETE("/db/table/delete", HandleDBDelete)
	api.GET("/db/mappings", HandleDBListMappings)
	api.POST("/db/mappings/auto", HandleDBAutoMapping)
	api.POST("/db/mappings/confirm", HandleDBConfirmMapping)
	api.PUT("/db/mappings/:id", HandleDBUpdateMapping)
	api.DELETE("/db/mappings/:id", HandleDBDeleteMapping)
	api.POST("/db/import/tasks", HandleDBCreateImportTask)
	api.GET("/db/import/tasks", HandleDBListImportTasks)
	api.GET("/db/import/tasks/:id", HandleDBGetImportTask)
	api.POST("/db/import/tasks/:id/start", HandleDBStartImportTask)
	api.POST("/db/import/tasks/:id/cancel", HandleDBCancelImportTask)
	api.GET("/db/import/tasks/:id/errors", HandleDBImportTaskErrors)
	api.GET("/db/import/tasks/:id/report", HandleDBImportTaskReport)
}

func HandleDBListConnections(c *gin.Context) {
	items, err := dbStore.ListConnections()
	if err != nil {
		dbError(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func HandleDBSaveConnection(c *gin.Context) {
	var payload dbimport.Connection
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid json"})
		return
	}
	item, err := dbStore.SaveConnection(payload)
	if err != nil {
		dbError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func HandleDBUpdateConnection(c *gin.Context) {
	var payload dbimport.Connection
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid json"})
		return
	}
	payload.ID = c.Param("id")
	item, err := dbStore.SaveConnection(payload)
	if err != nil {
		dbError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func HandleDBDeleteConnection(c *gin.Context) {
	if err := dbStore.DeleteConnection(c.Param("id")); err != nil {
		dbError(c, http.StatusNotFound, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func HandleDBTestUnsavedConnection(c *gin.Context) {
	var payload dbimport.Connection
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid json"})
		return
	}
	ctx, cancel := dbContext(c)
	defer cancel()
	if err := dbService.TestConnection(ctx, "", &payload); err != nil {
		dbError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func HandleDBTestConnection(c *gin.Context) {
	ctx, cancel := dbContext(c)
	defer cancel()
	if err := dbService.TestConnection(ctx, c.Param("id"), nil); err != nil {
		dbError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func HandleDBDatabases(c *gin.Context) {
	ctx, cancel := dbContext(c)
	defer cancel()
	items, err := dbService.Databases(ctx, c.Param("id"))
	if err != nil {
		dbError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func HandleDBSchemas(c *gin.Context) {
	ctx, cancel := dbContext(c)
	defer cancel()
	items, err := dbService.Schemas(ctx, c.Param("id"), c.Query("database"), c.Query("show_system") == "1")
	if err != nil {
		dbError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func HandleDBTables(c *gin.Context) {
	ctx, cancel := dbContext(c)
	defer cancel()
	items, err := dbService.Tables(ctx, dbimport.TableRef{
		ConnectionID: c.Param("id"),
		Database:     c.Query("database"),
		Schema:       c.Query("schema"),
	}, c.Query("show_system") == "1")
	if err != nil {
		dbError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func HandleDBColumns(c *gin.Context) {
	ctx, cancel := dbContext(c)
	defer cancel()
	items, err := dbService.Columns(ctx, dbTableRef(c))
	if err != nil {
		dbError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func HandleDBIndexes(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"items": []map[string]interface{}{}})
}

func HandleDBPreview(c *gin.Context) {
	var payload dbimport.TableDataRequest
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid json"})
		return
	}
	ctx, cancel := dbContext(c)
	defer cancel()
	resp, err := dbService.Preview(ctx, payload)
	if err != nil {
		dbError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func HandleDBSearch(c *gin.Context) {
	var payload dbimport.TableDataRequest
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid json"})
		return
	}
	ctx, cancel := dbContext(c)
	defer cancel()
	resp, err := dbService.Search(ctx, payload)
	if err != nil {
		dbError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func HandleDBQuery(c *gin.Context) {
	var payload dbimport.QueryRequest
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid json"})
		return
	}
	ctx, cancel := dbContext(c)
	defer cancel()
	resp, err := dbService.Query(ctx, payload)
	if err != nil {
		dbError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func HandleDBQueryCancel(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "request-scoped queries are cancelled when the HTTP request is aborted"})
}

func HandleDBInsert(c *gin.Context) {
	var payload dbimport.TableEditRequest
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid json"})
		return
	}
	ctx, cancel := dbContext(c)
	defer cancel()
	resp, err := dbService.InsertRow(ctx, payload)
	if err != nil {
		dbError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func HandleDBUpdate(c *gin.Context) {
	var payload dbimport.TableEditRequest
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid json"})
		return
	}
	ctx, cancel := dbContext(c)
	defer cancel()
	resp, err := dbService.UpdateRow(ctx, payload)
	if err != nil {
		dbError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func HandleDBDelete(c *gin.Context) {
	var payload dbimport.TableEditRequest
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid json"})
		return
	}
	ctx, cancel := dbContext(c)
	defer cancel()
	resp, err := dbService.DeleteRow(ctx, payload)
	if err != nil {
		dbError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func HandleDBListMappings(c *gin.Context) {
	items, err := dbStore.ListMappings(dbTableRef(c))
	if err != nil {
		dbError(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func HandleDBAutoMapping(c *gin.Context) {
	var payload dbimport.TableRef
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid json"})
		return
	}
	ctx, cancel := dbContext(c)
	defer cancel()
	rule, reused, err := dbService.AutoMapping(ctx, payload)
	if err != nil {
		dbError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"rule": rule, "reused": reused, "targetFields": dbimport.FlowTargetFields})
}

func HandleDBConfirmMapping(c *gin.Context) {
	var payload dbimport.MappingRule
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid json"})
		return
	}
	rule, err := dbStore.SaveMapping(payload)
	if err != nil {
		dbError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, rule)
}

func HandleDBUpdateMapping(c *gin.Context) {
	var payload dbimport.MappingRule
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid json"})
		return
	}
	payload.ID = c.Param("id")
	rule, err := dbStore.SaveMapping(payload)
	if err != nil {
		dbError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, rule)
}

func HandleDBDeleteMapping(c *gin.Context) {
	if err := dbStore.DeleteMapping(c.Param("id")); err != nil {
		dbError(c, http.StatusNotFound, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func HandleDBCreateImportTask(c *gin.Context) {
	var payload dbimport.ImportTaskRequest
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid json"})
		return
	}
	task, err := dbService.CreateTask(payload)
	if err != nil {
		dbError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, task)
}

func HandleDBListImportTasks(c *gin.Context) {
	items, err := dbStore.ListTasks()
	if err != nil {
		dbError(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func HandleDBGetImportTask(c *gin.Context) {
	task, err := dbStore.GetTask(c.Param("id"))
	if err != nil {
		dbError(c, http.StatusNotFound, err)
		return
	}
	c.JSON(http.StatusOK, task)
}

func HandleDBStartImportTask(c *gin.Context) {
	ctx, cancel := dbContext(c)
	defer cancel()
	task, err := dbService.StartTask(ctx, c.Param("id"))
	if err != nil {
		dbError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, task)
}

func HandleDBCancelImportTask(c *gin.Context) {
	task, err := dbStore.GetTask(c.Param("id"))
	if err != nil {
		dbError(c, http.StatusNotFound, err)
		return
	}
	if task.Status == "pending" || task.Status == "running" {
		task.Status = "canceled"
		task.UpdatedAt = time.Now()
		if err := dbStore.SaveTask(task); err != nil {
			dbError(c, http.StatusInternalServerError, err)
			return
		}
	}
	c.JSON(http.StatusOK, task)
}

func HandleDBImportTaskErrors(c *gin.Context) {
	task, err := dbStore.GetTask(c.Param("id"))
	if err != nil {
		dbError(c, http.StatusNotFound, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": task.Errors})
}

func HandleDBImportTaskReport(c *gin.Context) {
	task, err := dbStore.GetTask(c.Param("id"))
	if err != nil {
		dbError(c, http.StatusNotFound, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id":         task.ID,
		"name":       task.Name,
		"status":     task.Status,
		"progress":   task.Progress,
		"errors":     len(task.Errors),
		"session_id": task.SessionID,
	})
}

func dbContext(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), 30*time.Second)
}

func dbTableRef(c *gin.Context) dbimport.TableRef {
	return dbimport.TableRef{
		ConnectionID: c.Param("id"),
		Database:     c.Query("database"),
		Schema:       c.Query("schema"),
		Table:        c.Query("table"),
	}
}

func dbError(c *gin.Context, status int, err error) {
	c.JSON(status, gin.H{"detail": err.Error()})
}
