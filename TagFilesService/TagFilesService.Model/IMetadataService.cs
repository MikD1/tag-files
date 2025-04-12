namespace TagFilesService.Model;

public interface IMetadataService
{
    Task<FileMetadata> SaveMetadata(FileMetadata metadata);

    Task<FileMetadata> GetMetadata(uint id);

    Task<List<FileMetadata>> GetLastMetadataItems(int count);

    Task<List<FileMetadata>> GetUnprocessedMetadata();

    Task<FileMetadata> AssignTags(uint metadataId, List<uint> tagIds);

    Task<IPaginatedList<FileMetadata>> Search(string tagQuery, int pageIndex, int pageSize);
}