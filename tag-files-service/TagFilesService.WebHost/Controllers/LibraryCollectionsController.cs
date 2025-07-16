using Microsoft.AspNetCore.Mvc;
using TagFilesService.Library.Contracts;
using TagFilesService.Model;
using TagFilesService.WebHost.Dto.LibraryCollection;

namespace TagFilesService.WebHost.Controllers;

[ApiController]
[Route("api/library-collections")]
public class LibraryCollectionsController(ILibraryCollectionsRepository collectionsRepository) : ControllerBase
{
    [HttpGet]
    public async Task<ActionResult<List<LibraryCollectionDto>>> GetCollections()
    {
        List<LibraryCollection> collections = await collectionsRepository.GetCollections();
        List<LibraryCollectionDto> dto = collections.Select(LibraryCollectionDto.FromModel).ToList();
        return Ok(dto);
    }

    [HttpGet("{id}")]
    public async Task<ActionResult<LibraryCollectionWithItemsDto>> GetCollection(uint id)
    {
        LibraryCollection collection = await collectionsRepository.GetCollection(id);
        List<LibraryItem> collectionItems = await collectionsRepository.GetCollectionItems(id);
        LibraryCollectionWithItemsDto dto = new(
            collection.Id,
            collection.Name,
            collectionItems.Select(LibraryItemDto.FromModel).ToList());
        return Ok(dto);
    }

    [HttpPost]
    public async Task<ActionResult<LibraryCollectionDto>> CreateCollection([FromBody] CreateLibraryCollectionDto dto)
    {
        LibraryCollection collection = new(dto.Name);
        await collectionsRepository.SaveCollection(collection);
        LibraryCollectionDto createdDto = LibraryCollectionDto.FromModel(collection);
        return Ok(createdDto);
    }

    [HttpPut("{id}")]
    public async Task<ActionResult<LibraryCollectionDto>> UpdateCollection(uint id,
        [FromBody] UpdateLibraryCollectionDto dto)
    {
        LibraryCollection collection = await collectionsRepository.GetCollection(id);
        collection.Rename(dto.Name);
        await collectionsRepository.SaveCollection(collection);

        LibraryCollectionDto updatedDto = LibraryCollectionDto.FromModel(collection);
        return Ok(updatedDto);
    }

    [HttpDelete("{id}")]
    public async Task<ActionResult> DeleteCollection(uint id)
    {
        await collectionsRepository.DeleteCollection(id);
        return Ok();
    }
}