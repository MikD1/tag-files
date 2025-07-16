namespace TagFilesService.Model;

public interface ILibraryCollectionsRepository
{
    Task<LibraryCollection> SaveCollection(LibraryCollection collection);

    Task<List<LibraryCollection>> GetCollections();

    Task<LibraryCollection> GetCollection(uint id);

    Task DeleteCollection(uint id);

    Task<List<LibraryItem>> GetCollectionItems(uint collectionId);
}