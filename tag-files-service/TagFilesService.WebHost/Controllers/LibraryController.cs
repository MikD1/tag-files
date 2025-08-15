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

    [HttpDelete("{id}")]
    public async Task<IActionResult> Delete(uint id)
    {
        DeleteLibraryItemRequest request = new(id);
        await mediator.Send(request);
        return NoContent();
    }

    [HttpPost("{id}/toggle-favorite")]
    public async Task<ActionResult> ToggleFavorite(uint id)
    {
        ToggleFavoriteRequest request = new(id);
        await mediator.Send(request);
        return Ok();
    }

    [HttpGet("{id}/similar")]
    public async Task<ActionResult<List<LibraryItemDto>>> GetSimilar(uint id)
    {
        GetSimilarLibraryItemsRequest request = new(id);
        List<LibraryItemDto> result = await mediator.Send(request);
        return Ok(result);
    }

    [HttpPut("{id}/view-count")]
    public async Task<IActionResult> UpdateViewCount(uint id)
    {
        IncrementViewCountRequest request = new(id);
        await mediator.Send(request);
        return Ok();
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

    [HttpPost("assign-to-collection")]
    public async Task<ActionResult<List<LibraryItemDto>>> AssignToCollection(
        [FromBody] AssignItemsToCollectionRequest request)
    {
        if (request.ItemsList.Count == 0)
        {
            return BadRequest("Items list cannot be empty.");
        }

        if (request.CollectionId is 0)
        {
            request = request with { CollectionId = null };
        }

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