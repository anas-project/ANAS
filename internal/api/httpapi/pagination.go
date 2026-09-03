package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

type listPagination struct {
	Limit  int
	Cursor string
}

type listCursor struct {
	Version int    `json:"v"`
	Scope   string `json:"scope"`
	After   string `json:"after"`
}

func parseListPagination(w http.ResponseWriter, r *http.Request, extraQuery ...string) (listPagination, bool) {
	allowed := append(append([]string{}, extraQuery...), "limit", "cursor")
	query, ok := supportedQuery(w, r, allowed...)
	if !ok {
		return listPagination{}, false
	}
	pagination := listPagination{Limit: defaultPageLimit}
	if values, present := query["limit"]; present {
		if len(values) != 1 || values[0] == "" {
			writeProblem(w, http.StatusBadRequest, "invalid_limit", "limit must be a single integer between 1 and 100")
			return pagination, false
		}
		limit, err := strconv.Atoi(values[0])
		if err != nil || limit < 1 || limit > maximumPageLimit {
			writeProblem(w, http.StatusBadRequest, "invalid_limit", "limit must be an integer between 1 and 100")
			return pagination, false
		}
		pagination.Limit = limit
	}
	if values, present := query["cursor"]; present {
		if len(values) != 1 || values[0] == "" || len(values[0]) > maximumCursorLen {
			writeProblem(w, http.StatusBadRequest, "invalid_cursor", "cursor must be a single non-empty value no longer than 512 bytes")
			return pagination, false
		}
		pagination.Cursor = values[0]
	}
	return pagination, true
}

func paginateList[T any](items []T, pagination listPagination, scope string, key func(T) string) ([]T, *string, error) {
	start := 0
	if pagination.Cursor != "" {
		cursor, err := decodeListCursor(pagination.Cursor)
		if err != nil || cursor.Scope != listCursorScope(scope) {
			return nil, nil, errors.New("invalid cursor")
		}
		found := false
		for index, item := range items {
			if key(item) == cursor.After {
				start = index + 1
				found = true
				break
			}
		}
		if !found {
			return nil, nil, errors.New("invalid cursor")
		}
	}
	end := start + pagination.Limit
	if end > len(items) {
		end = len(items)
	}
	page := append([]T{}, items[start:end]...)
	if end == len(items) {
		return page, nil, nil
	}
	after := key(items[end-1])
	if after == "" {
		return nil, nil, errors.New("invalid pagination key")
	}
	next, err := encodeListCursor(listCursor{Version: 1, Scope: listCursorScope(scope), After: after})
	if err != nil {
		return nil, nil, err
	}
	return page, &next, nil
}

func writePaginatedListError(w http.ResponseWriter) {
	writeProblem(w, http.StatusBadRequest, "invalid_cursor", "cursor is invalid or no longer refers to this list")
}

func listCursorScope(scope string) string {
	digest := sha256.Sum256([]byte(scope))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func encodeListCursor(cursor listCursor) (string, error) {
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeListCursor(value string) (listCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return listCursor{}, err
	}
	var cursor listCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil {
		return listCursor{}, err
	}
	if cursor.Version != 1 || cursor.Scope == "" || cursor.After == "" {
		return listCursor{}, errors.New("invalid cursor")
	}
	canonical, err := encodeListCursor(cursor)
	if err != nil || canonical != value {
		return listCursor{}, errors.New("non-canonical cursor")
	}
	return cursor, nil
}
