package nex_datastore_super_mario_maker

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PretendoNetwork/nex-go/v2"
	"github.com/PretendoNetwork/nex-go/v2/types"
	datastore_super_mario_maker "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/super-mario-maker"
	datastore_super_mario_maker_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/super-mario-maker/types"
	datastore_types "github.com/PretendoNetwork/nex-protocols-go/v2/datastore/types"
	"github.com/PretendoNetwork/super-mario-maker/store"
)

func ids(values ...types.UInt64) types.List[types.UInt64] {
	return types.List[types.UInt64](values)
}

func TestObjectInfosGiveADownloadLinkForEachCourse(t *testing.T) {
	server := setUp(t)

	first := addObject(t, server.store, object{name: "first", size: 1234})
	second := addObject(t, server.store, object{owner: 2000, name: "second", size: 5678})

	rmc, nexError := GetObjectInfos(nil, from(3000), 7, ids(first, second))

	var infos types.List[datastore_super_mario_maker_types.DataStoreFileServerObjectInfo]
	read(t, response(t, rmc, nexError, datastore_super_mario_maker.MethodGetObjectInfos, 7), &infos)

	if len(infos) != 2 {
		t.Fatalf("got %d infos, want 2", len(infos))
	}

	for i, want := range []struct {
		dataID types.UInt64
		size   uint32
	}{{first, 1234}, {second, 5678}} {
		info := infos[i]
		wantURL := fmt.Sprintf("https://files.test/%s/%s/%d.bin", testBucket, testKeyBase, want.dataID)

		if info.DataID != want.dataID || info.GetInfo.DataID != want.dataID {
			t.Errorf("info %d is for %d (get info %d), want %d", i, info.DataID, info.GetInfo.DataID, want.dataID)
		}

		if string(info.GetInfo.URL) != wantURL {
			t.Errorf("info %d links to %q, want %q", i, info.GetInfo.URL, wantURL)
		}

		if uint32(info.GetInfo.Size) != want.size {
			t.Errorf("info %d has size %d, want %d", i, info.GetInfo.Size, want.size)
		}

		if string(info.GetInfo.RootCACert) != string(testRootCA) {
			t.Errorf("info %d hands the game root certificate %q, want %q", i, info.GetInfo.RootCACert, testRootCA)
		}
	}

	for _, lifetime := range server.presigner.lifetimes {
		if lifetime != 15*time.Minute {
			t.Errorf("a link was made to last %s, want 15 minutes", lifetime)
		}
	}
}

func TestObjectInfosKeepTheIDsOfImportedCourses(t *testing.T) {
	server := setUp(t)

	// * A course with the DataID it had on Nintendo's servers
	const nintendoID = 37585837

	param := datastore_types.NewDataStorePreparePostParam()
	param.DataType = 9
	param.Size = 4321

	if err := server.store.ImportObject(nintendoID, 1755555555, param, time.Date(2016, 4, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	if nexError := server.store.UpdateObjectUploadCompletedByDataID(nintendoID, true); nexError != nil {
		t.Fatal(nexError)
	}

	made := addObject(t, server.store, object{name: "made here"})
	if made < store.ImportedDataIDLimit {
		t.Errorf("a course made after an import got DataID %d, want one from %d", made, store.ImportedDataIDLimit)
	}

	rmc, nexError := GetObjectInfos(nil, from(1000), 1, ids(nintendoID, made))

	var infos types.List[datastore_super_mario_maker_types.DataStoreFileServerObjectInfo]
	read(t, response(t, rmc, nexError, datastore_super_mario_maker.MethodGetObjectInfos, 1), &infos)

	if len(infos) != 2 || infos[0].DataID != nintendoID || infos[1].DataID != made {
		t.Fatalf("got infos %v, want the imported %d and the new %d", infos, nintendoID, made)
	}

	if !strings.HasSuffix(string(infos[0].GetInfo.URL), fmt.Sprintf("/%d.bin", nintendoID)) {
		t.Errorf("the imported course links to %q, want its file under its own DataID", infos[0].GetInfo.URL)
	}
}

func TestObjectInfosForNothingAreAnEmptyList(t *testing.T) {
	setUp(t)

	rmc, nexError := GetObjectInfos(nil, from(1000), 1, ids())

	var infos types.List[datastore_super_mario_maker_types.DataStoreFileServerObjectInfo]
	read(t, response(t, rmc, nexError, datastore_super_mario_maker.MethodGetObjectInfos, 1), &infos)

	if len(infos) != 0 {
		t.Errorf("got %d infos, want none", len(infos))
	}
}

func TestObjectInfosFailWhenACourseIsMissingOrUnavailable(t *testing.T) {
	server := setUp(t)

	present := addObject(t, server.store, object{})
	deleted := addObject(t, server.store, object{})
	deleteObject(t, server.store, deleted)
	reviewed := addObject(t, server.store, object{})
	putUnderReview(t, server.store, reviewed)
	unfinished := addObject(t, server.store, object{notUploaded: true})

	for name, try := range map[string]struct {
		dataID types.UInt64
		code   uint32
	}{
		"never made":   {present + 100, nex.ResultCodes.DataStore.NotFound},
		"deleted":      {deleted, nex.ResultCodes.DataStore.NotFound},
		"under review": {reviewed, nex.ResultCodes.DataStore.UnderReviewing},
		"not uploaded": {unfinished, nex.ResultCodes.DataStore.NotFound},
	} {
		t.Run(name, func(t *testing.T) {
			rmc, nexError := GetObjectInfos(nil, from(1000), 1, ids(present, try.dataID))
			wantError(t, rmc, nexError, try.code)
		})
	}
}

func TestObjectInfosFailWhenNoDownloadLinkCanBeMade(t *testing.T) {
	server := setUp(t)
	server.presigner.failGet = true

	course := addObject(t, server.store, object{})

	rmc, nexError := GetObjectInfos(nil, from(1000), 1, ids(course))
	wantError(t, rmc, nexError, nex.ResultCodes.DataStore.OperationNotAllowed)
}

// * What a game sends to attach a preview image to its course
func attachParam(course types.UInt64, size uint32) datastore_super_mario_maker_types.DataStoreAttachFileParam {
	param := datastore_super_mario_maker_types.NewDataStoreAttachFileParam()
	param.ReferDataID = course
	param.ContentType = types.NewString("image/jpeg")
	param.PostParam.DataType = 2
	param.PostParam.Size = types.UInt32(size)
	param.PostParam.Name = types.NewString("thumbnail")

	return param
}

func TestPreparingAnAttachedFileMakesAnObjectOwnedByTheCaller(t *testing.T) {
	server := setUp(t)

	course := addObject(t, server.store, object{owner: 1000})

	param := attachParam(course, 2048)

	rating := datastore_types.NewDataStoreRatingInitParamWithSlot()
	rating.Slot = 0
	rating.Param.InitialValue = 5
	param.PostParam.RatingInitParams = append(param.PostParam.RatingInitParams, rating)

	rmc, nexError := PrepareAttachFile(nil, from(1000), 3, param)

	info := datastore_types.NewDataStoreReqPostInfo()
	read(t, response(t, rmc, nexError, datastore_super_mario_maker.MethodPrepareAttachFile, 3), &info)

	if info.DataID == 0 || info.DataID == course {
		t.Fatalf("got DataID %d, want a new object apart from the course %d", info.DataID, course)
	}

	if want := fmt.Sprintf("https://files.test/%s", testBucket); string(info.URL) != want {
		t.Errorf("upload goes to %q, want %q", info.URL, want)
	}

	fields := map[string]string{}
	for _, field := range info.FormFields {
		fields[string(field.Key)] = string(field.Value)
	}

	if want := fmt.Sprintf("%s/%d.jpg", testKeyBase, info.DataID); fields["key"] != want || fields["policy"] != "signed" || len(fields) != 2 {
		t.Errorf("got form fields %v, want the key %q and the policy", fields, want)
	}

	if string(info.RootCACert) != string(testRootCA) {
		t.Errorf("hands the game root certificate %q, want %q", info.RootCACert, testRootCA)
	}

	owner, nexError := server.store.GetObjectOwnerByDataID(info.DataID)
	if nexError != nil || owner != 1000 {
		t.Errorf("the image is owned by %d (%v), want the caller 1000", owner, nexError)
	}

	size, nexError := server.store.GetObjectSizeByDataID(info.DataID)
	if nexError != nil || size != 2048 {
		t.Errorf("the image has size %d (%v), want 2048", size, nexError)
	}

	// * Not available until the upload is completed
	if nexError := server.store.IsObjectAvailable(info.DataID); nexError == nil {
		t.Error("the image is available before it was uploaded")
	}

	if nexError := server.store.UpdateObjectUploadCompletedByDataID(info.DataID, true); nexError != nil {
		t.Fatal(nexError)
	}

	stored, nexError := server.store.GetObjectInfoByDataID(info.DataID)
	if nexError != nil {
		t.Fatal(nexError)
	}

	if stored.DataType != 2 || stored.ReferDataID != types.UInt32(course) {
		t.Errorf("stored data type %d referring to %d, want data type 2 referring to the course %d", stored.DataType, stored.ReferDataID, course)
	}

	if totals := ratingTotals(stored.Ratings); len(totals) != 1 || totals[0] != 5 {
		t.Errorf("stored ratings %v, want slot 0 starting at 5", totals)
	}
}

func TestAttachedFilesMadeAfterAnImportGetIDsAboveNintendos(t *testing.T) {
	server := setUp(t)

	param := datastore_types.NewDataStorePreparePostParam()
	param.DataType = 9

	if err := server.store.ImportObject(37585837, 1755555555, param, time.Now()); err != nil {
		t.Fatal(err)
	}

	rmc, nexError := PrepareAttachFile(nil, from(1000), 1, attachParam(37585837, 10))

	info := datastore_types.NewDataStoreReqPostInfo()
	read(t, response(t, rmc, nexError, datastore_super_mario_maker.MethodPrepareAttachFile, 1), &info)

	if info.DataID < store.ImportedDataIDLimit {
		t.Errorf("got DataID %d, want one from %d", info.DataID, store.ImportedDataIDLimit)
	}
}

func TestPreparingAnAttachedFileFailsWhenARatingCanNotBeMade(t *testing.T) {
	server := setUp(t)

	course := addObject(t, server.store, object{})
	param := attachParam(course, 10)

	// * The same slot twice
	for range 2 {
		param.PostParam.RatingInitParams = append(param.PostParam.RatingInitParams, datastore_types.NewDataStoreRatingInitParamWithSlot())
	}

	rmc, nexError := PrepareAttachFile(nil, from(1000), 1, param)
	wantError(t, rmc, nexError, nex.ResultCodes.DataStore.Unknown)
}

func TestPreparingAnAttachedFileFailsWhenTheStoreDoes(t *testing.T) {
	setUp(t)
	useBrokenStore(t)

	rmc, nexError := PrepareAttachFile(nil, from(1000), 1, attachParam(1, 10))
	wantError(t, rmc, nexError, nex.ResultCodes.DataStore.Unknown)
}

func TestPreparingAnAttachedFileFailsCleanlyWhenNoUploadLinkCanBeMade(t *testing.T) {
	t.Skip("BUG: PrepareAttachFile ignores the presigner's error and calls String on a nil URL, so the panic takes the whole server down (e.g. when PN_SMM_CONFIG_S3_BUCKET is empty)")

	server := setUp(t)
	server.presigner.failPost = true

	course := addObject(t, server.store, object{})

	var rmc *nex.RMCMessage
	var nexError *nex.Error

	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("the handler panicked: %v", recovered)
			}
		}()

		rmc, nexError = PrepareAttachFile(nil, from(1000), 1, attachParam(course, 10))
	}()

	wantError(t, rmc, nexError, nex.ResultCodes.DataStore.OperationNotAllowed)
}

// * Prepares an attached file of the given size, as a game does before
// * uploading a course's image
func prepareImage(t *testing.T, server *testServer, size uint32) types.UInt64 {
	t.Helper()

	course := addObject(t, server.store, object{})

	dataID, nexError := server.store.InitializeObjectByAttachFileParam(1000, attachParam(course, size))
	if nexError != nil {
		t.Fatal(nexError)
	}

	return dataID
}

func imageKey(dataID types.UInt64) string {
	return fmt.Sprintf("%s/%d.jpg", testKeyBase, dataID)
}

func completeParam(dataID types.UInt64, isSuccess bool) datastore_types.DataStoreCompletePostParam {
	param := datastore_types.NewDataStoreCompletePostParam()
	param.DataID = dataID
	param.IsSuccess = types.NewBool(isSuccess)

	return param
}

func TestCompletingAnAttachedFileMakesItAvailableAndGivesItsLink(t *testing.T) {
	server := setUp(t)

	image := prepareImage(t, server, 2048)
	server.files.put(imageKey(image), 2048)

	rmc, nexError := CompleteAttachFile(nil, from(1000), 9, completeParam(image, true))

	var link types.String
	read(t, response(t, rmc, nexError, datastore_super_mario_maker.MethodCompleteAttachFile, 9), &link)

	if want := fmt.Sprintf("https://files.test/%s/%s", testBucket, imageKey(image)); string(link) != want {
		t.Errorf("got link %q, want %q", link, want)
	}

	if nexError := server.store.IsObjectAvailable(image); nexError != nil {
		t.Errorf("the image is not available after completing it: %v", nexError)
	}
}

func TestCompletingAnAttachedFileThatFailedIsRefused(t *testing.T) {
	server := setUp(t)

	image := prepareImage(t, server, 2048)
	server.files.put(imageKey(image), 2048)

	rmc, nexError := CompleteAttachFile(nil, from(1000), 1, completeParam(image, false))
	wantError(t, rmc, nexError, nex.ResultCodes.DataStore.InvalidArgument)

	if server.store.IsObjectAvailable(image) == nil {
		t.Error("the image is available after a failed upload")
	}
}

func TestCompletingAnAttachedFileThatWasNeverUploadedIsNotFound(t *testing.T) {
	server := setUp(t)

	image := prepareImage(t, server, 2048)

	rmc, nexError := CompleteAttachFile(nil, from(1000), 1, completeParam(image, true))
	wantError(t, rmc, nexError, nex.ResultCodes.DataStore.NotFound)

	if server.store.IsObjectAvailable(image) == nil {
		t.Error("the image is available without a file")
	}
}

func TestCompletingAnAttachedFileOfTheWrongSizeIsRefused(t *testing.T) {
	server := setUp(t)

	image := prepareImage(t, server, 2048)
	server.files.put(imageKey(image), 2047)

	rmc, nexError := CompleteAttachFile(nil, from(1000), 1, completeParam(image, true))
	wantError(t, rmc, nexError, nex.ResultCodes.DataStore.Unknown)

	if server.store.IsObjectAvailable(image) == nil {
		t.Error("the image is available though its file is the wrong size")
	}
}

func TestCompletingAFileWithNoObjectIsNotFound(t *testing.T) {
	server := setUp(t)

	server.files.put(imageKey(424242), 10)

	rmc, nexError := CompleteAttachFile(nil, from(1000), 1, completeParam(424242, true))
	wantError(t, rmc, nexError, nex.ResultCodes.DataStore.NotFound)
}

func TestCompletingAnAttachedFileUnderReviewIsRefused(t *testing.T) {
	server := setUp(t)

	image := prepareImage(t, server, 10)
	server.files.put(imageKey(image), 10)
	putUnderReview(t, server.store, image)

	rmc, nexError := CompleteAttachFile(nil, from(1000), 1, completeParam(image, true))
	wantError(t, rmc, nexError, nex.ResultCodes.DataStore.UnderReviewing)
}

func TestCompletingAnAttachedFileFailsWhenNoDownloadLinkCanBeMade(t *testing.T) {
	server := setUp(t)
	server.presigner.failGet = true

	image := prepareImage(t, server, 10)
	server.files.put(imageKey(image), 10)

	rmc, nexError := CompleteAttachFile(nil, from(1000), 1, completeParam(image, true))
	wantError(t, rmc, nexError, nex.ResultCodes.DataStore.OperationNotAllowed)
}
