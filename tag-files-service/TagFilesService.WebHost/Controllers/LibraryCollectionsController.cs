using MediatR;
using Microsoft.AspNetCore.Mvc;
using TagFilesService.Library.Contracts.LibraryCollections;

namespace TagFilesService.WebHost.Controllers;

[ApiController]
[Route("api/library-collections")]
public class LibraryCollectionsController(IMediator mediator)
    : ControllerBase
{
    [HttpGet]
    public async Task<ActionResult<List<LibraryCollectionDto>>> GetCollections()
    {
        GetLibraryCollectionsRequest request = new();
        List<LibraryCollectionDto> collections = await mediator.Send(request);
        return Ok(collections);
    }

    [HttpGet("{id}")]
    public async Task<ActionResult<LibraryCollectionWithItemsDto>> GetCollection(uint id)
    {
        GetLibraryCollectionRequest request = new(id);
        LibraryCollectionWithItemsDto collection = await mediator.Send(request);
        return Ok(collection);
    }

    [HttpPost]
    public async Task<ActionResult<LibraryCollectionDto>> CreateCollection(
        [FromBody] CreateLibraryCollectionRequest request)
    {
        LibraryCollectionDto collection = await mediator.Send(request);
        return Ok(collection);
    }

    [HttpPut]
    public async Task<ActionResult<LibraryCollectionDto>> UpdateCollection(
        [FromBody] UpdateLibraryCollectionRequest request)
    {
        LibraryCollectionDto collection = await mediator.Send(request);
        return Ok(collection);
    }

    [HttpDelete("{id}")]
    public async Task<ActionResult> DeleteCollection(uint id)
    {
        DeleteLibraryCollectionRequest request = new(id);
        await mediator.Send(request);
        return Ok();
    }
}