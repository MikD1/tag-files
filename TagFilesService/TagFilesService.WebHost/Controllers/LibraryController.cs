using Microsoft.AspNetCore.Mvc;
using TagFilesService.Model;
using TagFilesService.WebHost.Dto;

namespace TagFilesService.WebHost.Controllers;

[ApiController]
[Route("api/library")]
public class LibraryController(FileStorage fileStorage, IMetadataService metadataService) : ControllerBase
{
    [HttpPost("upload")]
    public async Task<ActionResult<LibraryItemDto>> Upload(IFormFile file)
    {
        if (file.Length == 0)
        {
            return BadRequest("No file uploaded");
        }

        string fileExtension = Path.GetExtension(file.FileName);
        await using Stream stream = file.OpenReadStream();
        string fileName =
            await fileStorage.UploadFile("library", stream, file.Length, file.ContentType, null, fileExtension);

        // TODO: Make thumbnail from the file
        await fileStorage.UploadFile("thumbnail", stream, file.Length, file.ContentType, null, fileExtension);

        // TODO: get file type
        FileMetadata metadata = new(fileName, FileType.Image, null);
        await metadataService.SaveMetadata(metadata);
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
}