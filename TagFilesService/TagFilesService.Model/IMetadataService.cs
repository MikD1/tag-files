namespace TagFilesService.Model;

public interface IMetadataService
{
    Task<FileMetadata> SaveMetadata(FileMetadata metadata);

    Task<FileMetadata> GetMetadata(uint id);

    Task<List<FileMetadata>> GetLastMetadataItems(int count);

    Task<FileMetadata> AssignTags(uint metadataId, List<uint> tagIds);

    Task<List<FileMetadata>> Search(string tagQuery, int count);
}