package manage

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ItemName returns an item's display name.
func (c *EmbyClient) ItemName(itemID string) (string, error) {
	var page struct {
		Items []struct {
			Name string `json:"Name"`
		} `json:"Items"`
	}
	q := url.Values{}
	q.Set("Ids", itemID)
	if err := c.do(http.MethodGet, "/Items", q, nil, &page); err != nil {
		return "", err
	}
	if len(page.Items) == 0 {
		return "", fmt.Errorf("item %s not found", itemID)
	}
	return page.Items[0].Name, nil
}

// MovePeopleLinks re-points one movie's person links from fromIDs to the target
// person. It is the native equivalent of the AvdbMagicTools "TransferPerson"
// call: read the DTO, rewrite its People list, write it back.
//
// Returns true when the movie actually changed.
func (c *EmbyClient) MovePeopleLinks(movieID string, fromIDs []string, toID, toName string) (bool, error) {
	dto, err := c.itemDTO(movieID)
	if err != nil {
		return false, err
	}
	people, ok := dto["People"].([]any)
	if !ok || len(people) == 0 {
		return false, nil
	}
	from := make(map[string]struct{}, len(fromIDs))
	for _, id := range fromIDs {
		from[id] = struct{}{}
	}

	hasTarget := false
	for _, p := range people {
		if m, ok := p.(map[string]any); ok {
			if id, _ := m["Id"].(string); id == toID {
				hasTarget = true
			}
		}
	}

	rebuilt := make([]any, 0, len(people))
	changed := false
	for _, p := range people {
		m, ok := p.(map[string]any)
		if !ok {
			rebuilt = append(rebuilt, p)
			continue
		}
		id, _ := m["Id"].(string)
		if _, drop := from[id]; !drop {
			rebuilt = append(rebuilt, m)
			continue
		}
		changed = true
		if hasTarget {
			continue // target already on this movie: just drop the duplicate
		}
		// Keep Role/Type/Image but point at the surviving person.
		m["Id"] = toID
		m["Name"] = toName
		hasTarget = true
		rebuilt = append(rebuilt, m)
	}
	if !changed {
		return false, nil
	}
	dto["People"] = rebuilt
	if err := c.do(http.MethodPost, "/Items/"+url.PathEscape(movieID), nil, dto, nil); err != nil {
		return false, err
	}
	return true, nil
}

// DeleteItem is a best-effort item delete. Emby accepts the request for virtual
// items (persons) but only removes them during a library scan, so callers must
// treat "still exists" as normal and rely on TriggerLibraryScan.
func (c *EmbyClient) DeleteItem(itemID string) error {
	return c.do(http.MethodDelete, "/Items/"+url.PathEscape(itemID), nil, nil, nil)
}

// TriggerLibraryScan starts Emby's "Scan media library" task. The scan is what
// actually prunes persons that no longer have any linked item.
func (c *EmbyClient) TriggerLibraryScan() (bool, error) {
	var tasks []struct {
		Id  string `json:"Id"`
		Key string `json:"Key"`
	}
	if err := c.do(http.MethodGet, "/ScheduledTasks", nil, nil, &tasks); err != nil {
		return false, err
	}
	for _, t := range tasks {
		if !strings.EqualFold(t.Key, "RefreshLibrary") {
			continue
		}
		err := c.do(http.MethodPost, "/ScheduledTasks/Running/"+url.PathEscape(t.Id), nil, nil, nil)
		return err == nil, err
	}
	return false, fmt.Errorf("emby 未找到媒体库扫描任务")
}
