using Microsoft.AspNetCore.Mvc;
using TagFilesService.Model;
using TagFilesService.Thumbnail;
using TagFilesService.WebHost.Dto;

namespace TagFilesService.WebHost.Controllers;

[ApiController]
[Route("api/library")]
public class LibraryController(
    IFileStorage fileStorage,
    IMetadataService metadataService,
    IThumbnailService thumbnailService) : ControllerBase
{
    [HttpPost("upload")]
    public async Task<ActionResult<LibraryItemDto>> Upload(List<IFormFile> files)
    {
        // TOTO: use presigned urls for upload
        if (files.Count == 0)
        {
            return BadRequest("No files selected");
        }

        foreach (IFormFile file in files)
        {
            if (file.Length == 0)
            {
                continue;
            }

            string fileName = MakeLibraryFileName(file.FileName);
            await using Stream stream = file.OpenReadStream();
            await fileStorage.UploadFile("library", fileName, stream, file.Length, file.ContentType);

            // TODO: get file type
            FileMetadata metadata = new(fileName, FileType.Unknown, null);
            await metadataService.SaveMetadata(metadata);
        }

        thumbnailService.StartThumbnailsGeneration();
        return Ok();
    }

    [HttpPost("search")]
    public async Task<ActionResult<IPaginatedList<LibraryItemDto>>> Search(
        [FromBody] SearchParametersDto searchParameters)
    {
        IPaginatedList<FileMetadata> searchResults = await metadataService.Search(searchParameters.TagQuery,
            searchParameters.PageIndex, searchParameters.PageSize);

        List<LibraryItemDto> itemsDto = searchResults.Items
            .Select(LibraryItemDto.FromMetadata)
            .ToList();
        return Ok(new PaginatedListDto<LibraryItemDto>(itemsDto, searchResults.TotalItems, searchResults.PageIndex,
            searchResults.TotalPages));
    }

    [HttpPost("generate-upload-urls")]
    public async Task<ActionResult<List<string>>> GenerateUploadUrls([FromBody] List<string> fileNames)
    {
        List<string> result = [];
        foreach (string fileName in fileNames)
        {
            string libraryFileName = MakeLibraryFileName(fileName);
            string url = await fileStorage.GeneratePresignedUrl("temporary", libraryFileName, TimeSpan.FromHours(1));
            result.Add(url);
        }

        return Ok(result);
    }

    [HttpPost("upload-complete")]
    public ActionResult UploadComplete()
    {
        // move files from temporary bucket to library bucket
        // add metadata
        // generate thumbnails
        return Ok();
    }

    private string MakeLibraryFileName(string originalName)
    {
        string extension = Path.GetExtension(originalName).ToLower();
        string fileName = Guid.NewGuid()
            .ToString()
            .Replace("-", string.Empty)
            .ToLower() + extension;
        return fileName;
    }
}