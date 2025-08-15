namespace TagFilesService.Model.Exceptions;

public class NotFoundException(string entityName, string entiryId)
    : ApplicationException($"Entity '{entityName}' with id '{entiryId}' not found");