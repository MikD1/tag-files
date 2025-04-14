using MediatR;
using Microsoft.AspNetCore.Mvc;
using TagFilesService.Library.Contracts;
using TagFilesService.Model;

namespace TagFilesService.WebHost.Controllers;

[ApiController]
[Route("api/library")]
public class LibraryController(IMediator mediator) : ControllerBase
{
    [HttpPost("search")]
    public async Task<ActionResult<PaginatedList<LibraryItemDto>>> Search([FromBody] SearchRequest request)
    {
        PaginatedList<LibraryItemDto> result = await mediator.Send(request);
        return Ok(result);
    }

    [HttpPost("generate-upload-urls")]
    public async Task<ActionResult<List<string>>> GenerateUploadUrls([FromBody] GeneratePresignedUrlsRequest request)
    {
        Dictionary<string, string> result = await mediator.Send(request);
        return Ok(result);
    }
}