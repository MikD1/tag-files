using Microsoft.AspNetCore.Mvc;
using TagFilesService.Model;
using TagFilesService.WebHost.Dto;

namespace TagFilesService.WebHost.Controllers;

[ApiController]
[Route("api/library")]
public class LibraryController(
    IFileStorage fileStorage,
    IMetadataService metadataService) : ControllerBase
{
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