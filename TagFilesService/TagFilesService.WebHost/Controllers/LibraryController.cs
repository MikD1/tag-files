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
    public async Task<ActionResult<LibraryItemDto>> Upload(IFormFile file)
    {
        if (file.Length == 0)
        {
            return BadRequest("No file uploaded");
        }

        string fileExtension = Path.GetExtension(file.FileName).ToLower();
        string fileName = MakeLibraryFileName(fileExtension);
        await using Stream stream = file.OpenReadStream();
        await fileStorage.UploadFile("library", fileName, stream, file.Length, file.ContentType);

        // TODO: get file type
        FileMetadata metadata = new(fileName, FileType.Unknown, null);
        metadata = await metadataService.SaveMetadata(metadata);

        thumbnailService.EnqueueThumbnailGeneration(metadata.Id);

        return LibraryItemDto.FromMetadata(metadata);
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

    private string MakeLibraryFileName(string extension)
    {
        string fileName = Guid.NewGuid()
            .ToString()
            .Replace("-", string.Empty)
            .ToLower() + extension;
        return fileName;
    }
}