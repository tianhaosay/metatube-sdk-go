package manage

import (
	"fmt"
	"net/http"
	"net/url"
)

// FetchMovies returns a page of movies from a library (or from every library
// when libraryID is empty) with the fields the audit needs.
func (c *EmbyClient) FetchMovies(libraryID string, start, limit int) ([]embyItem, int, error) {
	q := url.Values{}
	if libraryID != "" {
		q.Set("ParentId", libraryID)
	}
	q.Set("Recursive", "true")
	q.Set("IncludeItemTypes", "Movie")
	q.Set("Fields", "Genres,People,Overview,ImageTags,ProviderIds,Path,ProductionYear")
	q.Set("StartIndex", fmt.Sprint(start))
	q.Set("Limit", fmt.Sprint(limit))

	var page struct {
		Items            []embyItem `json:"Items"`
		TotalRecordCount int        `json:"TotalRecordCount"`
	}
	if err := c.do(http.MethodGet, "/Items", q, nil, &page); err != nil {
		return nil, 0, err
	}
	return page.Items, page.TotalRecordCount, nil
}

// RefreshMetadata asks Emby to re-scrape one item.
//
// ReplaceAllMetadata/ReplaceAllImages are pinned to false: "replace all" is
// the mode that overwrote local (Chinese) metadata with provider output.
func (c *EmbyClient) RefreshMetadata(itemID string) error {
	q := url.Values{}
	q.Set("MetadataRefreshMode", "FullRefresh")
	q.Set("ImageRefreshMode", "Default")
	q.Set("ReplaceAllMetadata", "false")
	q.Set("ReplaceAllImages", "false")
	q.Set("Recursive", "false")
	return c.do(http.MethodPost, "/Items/"+url.PathEscape(itemID)+"/Refresh", q, nil, nil)
}

// itemDTO loads a full item DTO.
//
// Emby only returns the full DTO (the shape POST /Items/{id} accepts) through
// the user-scoped endpoint; /Items?Ids= returns a partial object that the
// update endpoint rejects with "Value cannot be null".
func (c *EmbyClient) itemDTO(itemID string) (map[string]any, error) {
	if uid := c.resolveUserID(); uid != "" {
		var dto map[string]any
		err := c.do(http.MethodGet,
			"/Users/"+url.PathEscape(uid)+"/Items/"+url.PathEscape(itemID), nil, nil, &dto)
		if err == nil && len(dto) > 0 {
			return dto, nil
		}
	}

	var page struct {
		Items []map[string]any `json:"Items"`
	}
	q := url.Values{}
	q.Set("Ids", itemID)
	q.Set("Fields", "Name,Overview,SortName")
	if err := c.do(http.MethodGet, "/Items", q, nil, &page); err != nil {
		return nil, err
	}
	if len(page.Items) == 0 {
		return nil, fmt.Errorf("item %s not found", itemID)
	}
	return page.Items[0], nil
}

// RenameItem changes an item's display name (used to canonicalize actor
// names without any Emby plugin). Emby applies the change live.
func (c *EmbyClient) RenameItem(itemID, name string) error {
	dto, err := c.itemDTO(itemID)
	if err != nil {
		return err
	}
	dto["Name"] = name
	dto["SortName"] = name
	return c.do(http.MethodPost, "/Items/"+url.PathEscape(itemID), nil, dto, nil)
}

// UpdatePersonFields patches an item (usually a Person) with the given fields
// and writes it back. Used for biography / TMDB id completion.
func (c *EmbyClient) UpdatePersonFields(itemID string, patch map[string]any) error {
	dto, err := c.itemDTO(itemID)
	if err != nil {
		return err
	}
	for k, v := range patch {
		dto[k] = v
	}
	return c.do(http.MethodPost, "/Items/"+url.PathEscape(itemID), nil, dto, nil)
}
