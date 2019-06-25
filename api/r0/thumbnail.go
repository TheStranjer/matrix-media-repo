package r0

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"github.com/turt2live/matrix-media-repo/api"
	"github.com/turt2live/matrix-media-repo/common"
	"github.com/turt2live/matrix-media-repo/common/config"
	"github.com/turt2live/matrix-media-repo/controllers/thumbnail_controller"
	"github.com/turt2live/matrix-media-repo/types"
	"github.com/turt2live/matrix-media-repo/util"
	"github.com/turt2live/matrix-media-repo/util/singleflight-counter"
)

var thumbnailRequestGroup singleflight_counter.Group

func ThumbnailMedia(r *http.Request, log *logrus.Entry, user api.UserInfo) interface{} {
	params := mux.Vars(r)

	server := params["server"]
	mediaId := params["mediaId"]
	allowRemote := r.URL.Query().Get("allow_remote")

	downloadRemote := true
	if allowRemote != "" {
		parsedFlag, err := strconv.ParseBool(allowRemote)
		if err != nil {
			return api.InternalServerError("allow_remote flag does not appear to be a boolean")
		}
		downloadRemote = parsedFlag
	}

	log = log.WithFields(logrus.Fields{
		"mediaId":     mediaId,
		"server":      server,
		"allowRemote": downloadRemote,
	})

	widthStr := r.URL.Query().Get("width")
	heightStr := r.URL.Query().Get("height")
	method := r.URL.Query().Get("method")
	animatedStr := r.URL.Query().Get("animated")

	width := config.Get().Thumbnails.Sizes[0].Width
	height := config.Get().Thumbnails.Sizes[0].Height
	animated := config.Get().Thumbnails.AllowAnimated && config.Get().Thumbnails.DefaultAnimated

	if widthStr != "" {
		parsedWidth, err := strconv.Atoi(widthStr)
		if err != nil {
			return api.InternalServerError("Width does not appear to be an integer")
		}
		width = parsedWidth
	}
	if heightStr != "" {
		parsedHeight, err := strconv.Atoi(heightStr)
		if err != nil {
			return api.InternalServerError("Height does not appear to be an integer")
		}
		height = parsedHeight
	}
	if animatedStr != "" {
		parsedFlag, err := strconv.ParseBool(animatedStr)
		if err != nil {
			return api.InternalServerError("Animated flag does not appear to be a boolean")
		}
		animated = parsedFlag
	}
	if method == "" {
		method = "scale"
	}

	log = log.WithFields(logrus.Fields{
		"requestedWidth":    width,
		"requestedHeight":   height,
		"requestedMethod":   method,
		"requestedAnimated": animated,
	})

	// TODO: Move this to a lower layer (somewhere where the thumbnail dimensions are known, before media is downloaded)
	requestKey := fmt.Sprintf("thumbnail_%s_%s_%d_%d_%s_%t", server, mediaId, width, height, method, animated)
	v, count, err := thumbnailRequestGroup.Do(requestKey, func() (interface{}, error) {
		streamedThumbnail, err := thumbnail_controller.GetThumbnail(server, mediaId, width, height, animated, method, downloadRemote, r.Context(), log)
		if err != nil {
			if err == common.ErrMediaNotFound {
				return api.NotFoundError(), nil
			} else if err == common.ErrMediaTooLarge {
				return api.RequestTooLarge(), nil
			}
			log.Error("Unexpected error locating media: " + err.Error())
			return api.InternalServerError("Unexpected Error"), nil
		}

		return streamedThumbnail, nil
	}, func(v interface{}, count int, err error) []interface{} {
		if err != nil {
			return nil
		}

		rv := v.(*types.StreamedThumbnail)
		vals := make([]interface{}, 0)
		streams := util.CloneReader(rv.Stream, count)

		for i := 0; i < count; i++ {
			vals = append(vals, &types.StreamedThumbnail{
				Thumbnail: rv.Thumbnail,
				Stream:    streams[i],
			})
		}

		return vals
	})

	if err != nil {
		log.Error("Unexpected error handling request: " + err.Error())
		return api.InternalServerError("Unexpected Error")
	}

	rv := v.(*types.StreamedThumbnail)

	if count > 0 {
		log.Info("Request response was shared ", count, " times")
	}

	return &DownloadMediaResponse{
		ContentType: rv.Thumbnail.ContentType,
		SizeBytes:   rv.Thumbnail.SizeBytes,
		Data:        rv.Stream,
		Filename:    "thumbnail",
	}
}
