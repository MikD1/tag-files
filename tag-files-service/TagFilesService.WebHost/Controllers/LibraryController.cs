using MediatR;
using Microsoft.AspNetCore.Mvc;
using TagFilesService.Library.Contracts;
using TagFilesService.Model;

namespace TagFilesService.WebHost.Controllers;

[ApiController]
[Route("api/library")]
public class LibraryController(IMediator mediator) : ControllerBase
{
    [HttpGet("{id}")]
    public async Task<ActionResult<LibraryItemDto>> Get(uint id)
    {
        GetLibraryItemByIdRequest request = new(id);
        LibraryItemDto? result = await mediator.Send(request);
        if (result is null)
        {
            return NotFound();
        }

        return result;
    }

    [HttpPost("search")]
    public async Task<ActionResult<PaginatedList<LibraryItemDto>>> Search([FromBody] SearchRequest request)
    {
        PaginatedList<LibraryItemDto> result = await mediator.Send(request);
        return Ok(result);
    }

    [HttpPost("assign-tags")]
    public async Task<ActionResult<List<LibraryItemDto>>> AssignTags([FromBody] AssignTagsRequest request)
    {
        List<LibraryItemDto> result = await mediator.Send(request);
        return Ok(result);
    }

    [HttpPost("generate-upload-urls")]
    public async Task<ActionResult<List<string>>> GenerateUploadUrls([FromBody] GeneratePresignedUrlsRequest request)
    {
        Dictionary<string, string> result = await mediator.Send(request);
        return Ok(result);
    }
}