using Microsoft.EntityFrameworkCore;
using TagFilesService.Model;

namespace TagFilesService.Infrastructure;

public class LibraryCollectionsRepository(AppDbContext dbContext) : ILibraryCollectionsRepository
{
    public async Task<LibraryCollection> SaveCollection(LibraryCollection collection)
    {
        if (collection.Id == 0)
        {
            dbContext.Collections.Add(collection);
        }
        else
        {
            dbContext.Collections.Update(collection);
        }

        await dbContext.SaveChangesAsync();
        return collection;
    }

    public async Task<List<LibraryCollection>> GetCollections()
    {
        return await dbContext.Collections
            .OrderBy(x => x.Name)
            .ToListAsync();
    }

    public async Task<LibraryCollection> GetCollection(uint id)
    {
        return await GetCollectionByIdOrThrow(id);
    }

    public async Task DeleteCollection(uint id)
    {
        LibraryCollection collection = await GetCollectionByIdOrThrow(id);
        dbContext.Collections.Remove(collection);
        await dbContext.SaveChangesAsync();
    }

    public async Task<List<LibraryItem>> GetCollectionItems(uint collectionId)
    {
        return await dbContext.LibraryItems
            .Where(x => x.CollectionId == collectionId)
            .Include(x => x.Tags)
            .OrderByDescending(x => x.UploadedOn)
            .ToListAsync();
    }

    private async Task<LibraryCollection> GetCollectionByIdOrThrow(uint id)
    {
        LibraryCollection? collection = await dbContext.Collections.FirstOrDefaultAsync(x => x.Id == id);
        if (collection is null)
        {
            throw new ApplicationException($"LibraryCollection {id} not found");
        }

        return collection;
    }
}