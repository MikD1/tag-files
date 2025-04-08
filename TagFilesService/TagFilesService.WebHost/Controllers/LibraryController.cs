using Microsoft.AspNetCore.Mvc;
using TagFilesService.Model;
using TagFilesService.WebHost.Dto;

namespace TagFilesService.WebHost.Controllers;

[ApiController]
[Route("api/library")]
public class LibraryController(IMetadataService metadataService) : ControllerBase
{
    [HttpPost("search")]
    public async Task<ActionResult<IPaginatedList<LibraryItemDto>>> Search(
        [FromBody] SearchParametersDto searchParameters)
    {
        IPaginatedList<FileMetadata> searchResults = await metadataService.Search(searchParameters.TagQuery,
            searchParameters.PageIndex, searchParameters.PageSize);

        // TODO: Get full_path and full_thumbnail_path from minio
        List<LibraryItemDto> itemsDto = searchResults.Items
            .Select(x => new LibraryItemDto("full_path", "full_thumbnail_path", x.Description, x.UploadedOn,
                x.Tags.Select(t => t.Name).ToList()))
            .ToList();
        return Ok(new PaginatedListDto<LibraryItemDto>(itemsDto, searchResults.TotalItems, searchResults.PageIndex,
            searchResults.TotalPages));
    }
}