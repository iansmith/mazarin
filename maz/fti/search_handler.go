package main

import (
	"fmt"
	"unsafe"

	"github.com/blevesearch/bleve/v2"

	"mazzy/mazarin/mem"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/fti"
	"mazzy/shared/ipc"
)

// searchHandler processes SearchMail requests from maildb.
type searchHandler struct {
	index bleve.Index
}

func newSearchHandler(index bleve.Index) *searchHandler {
	return &searchHandler{index: index}
}

// handleSearchMail runs a bleve query for the given SearchMail request and
// sends a SearchResult (with an optional page of SearchResultEntry records)
// back to the requester.
//
// Size=0 means count-only: Total is returned but no page is allocated.
func (h *searchHandler) handleSearchMail(req *fti.SearchMail, senderSID int16) {
	query := fti.UnpackSearchQuery(req)
	fmt.Printf("[fti] SearchMail: type=%d query=%q from=%d size=%d SID=%d\n",
		req.QueryType, query, req.From, req.Size, senderSID)

	// Build bleve query based on QueryType.
	buildQuery := func() *bleve.SearchRequest {
		switch req.QueryType {
		case fti.QueryTypeSubject:
			q := bleve.NewMatchQuery(query)
			q.SetField("subject")
			sr := bleve.NewSearchRequest(q)
			if req.SortOrder == fti.SortAsc {
				sr.SortBy([]string{"date"})
			} else {
				sr.SortBy([]string{"-date"})
			}
			return sr
		case fti.QueryTypeFrom:
			q := bleve.NewMatchQuery(query)
			q.SetField("from")
			sr := bleve.NewSearchRequest(q)
			if req.SortOrder == fti.SortAsc {
				sr.SortBy([]string{"date"})
			} else {
				sr.SortBy([]string{"-date"})
			}
			return sr
		default:
			return nil
		}
	}

	// Count-only: Size=0. Run a size-0 search to get the total.
	if req.Size == 0 {
		sr := buildQuery()
		if sr == nil {
			e := fti.PackSearchError(req.RequestId, 1, fmt.Sprintf("unknown QueryType %d", req.QueryType))
			msg := fti.EncodeSearchError(&e)
			sendFTIMsg(int(senderSID), &msg)
			return
		}
		sr.Size = 0
		result, err := h.index.Search(sr)
		if err != nil {
			fmt.Printf("[fti] SearchMail count: %v\n", err)
			e := fti.PackSearchError(req.RequestId, 1, err.Error())
			msg := fti.EncodeSearchError(&e)
			sendFTIMsg(int(senderSID), &msg)
			return
		}
		resp := fti.SearchResult{
			RequestId: req.RequestId,
			Total:     uint32(result.Total),
		}
		msg := fti.EncodeSearchResult(&resp)
		sendFTIMsg(int(senderSID), &msg)
		return
	}

	// Full search with results.
	searchReq := buildQuery()
	if searchReq == nil {
		e := fti.PackSearchError(req.RequestId, 1, fmt.Sprintf("unknown QueryType %d", req.QueryType))
		msg := fti.EncodeSearchError(&e)
		sendFTIMsg(int(senderSID), &msg)
		return
	}
	searchReq.Size = int(req.Size)
	searchReq.From = int(req.From)

	result, err := h.index.Search(searchReq)
	if err != nil {
		fmt.Printf("[fti] SearchMail search: %v\n", err)
		e := fti.PackSearchError(req.RequestId, 1, err.Error())
		msg := fti.EncodeSearchError(&e)
		sendFTIMsg(int(senderSID), &msg)
		return
	}

	count := len(result.Hits)
	if count == 0 {
		resp := fti.SearchResult{
			RequestId: req.RequestId,
			Total:     uint32(result.Total),
			ErrCode:   0,
		}
		msg := fti.EncodeSearchResult(&resp)
		sendFTIMsg(int(senderSID), &msg)
		return
	}

	// Allocate pages for SearchResultEntry records.
	numPages := (count*fti.SearchResultEntrySize + 4095) / 4096
	pages, allocErr := mem.AllocPagesSlice(numPages, mem.PageShared)
	if allocErr != nil {
		fmt.Printf("[fti] SearchMail AllocPages(%d): %v\n", numPages, allocErr)
		e := fti.PackSearchError(req.RequestId, 1, "alloc failed")
		msg := fti.EncodeSearchError(&e)
		sendFTIMsg(int(senderSID), &msg)
		return
	}

	// Pack SearchResultEntry records into the pages.
	for i, hit := range result.Hits {
		entry := fti.SearchResultEntry{}
		n := copy(entry.DocId[:], hit.ID)
		entry.IdLen = uint16(n)
		*(*fti.SearchResultEntry)(unsafe.Pointer(&pages[i*fti.SearchResultEntrySize])) = entry
	}

	pagePtr := unsafe.Pointer(&pages[0])
	targetVA, transErr := sys.TransferAndUnmap(int(senderSID), uintptr(pagePtr), numPages)
	if transErr != nil {
		_ = mem.FreePages(pagePtr, numPages)
		fmt.Printf("[fti] SearchMail TransferAndUnmap: %v\n", transErr)
		e := fti.PackSearchError(req.RequestId, 1, "transfer failed")
		msg := fti.EncodeSearchError(&e)
		sendFTIMsg(int(senderSID), &msg)
		return
	}

	resp := fti.SearchResult{
		RequestId: req.RequestId,
		TargetVA:  uint64(targetVA),
		NumBytes:  uint32(count * fti.SearchResultEntrySize),
		Count:     uint32(count),
		Total:     uint32(result.Total),
		ErrCode:   0,
	}
	msg := fti.EncodeSearchResult(&resp)
	sendFTIMsg(int(senderSID), &msg)
	fmt.Printf("[fti] SearchMail: returned %d/%d hits\n", count, result.Total)
}

func sendFTIMsg(targetSID int, msg *ipc.UringIPCMsg) {
	if err := uring.Send(targetSID, msg); err != nil {
		fmt.Printf("[fti] send to SID %d failed: %v\n", targetSID, err)
	}
}
